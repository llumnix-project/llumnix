package policy

import (
	"context"
	"errors"
	"fmt"
	"llumnix/pkg/cms"
	"llumnix/pkg/metrics"
	"llumnix/pkg/scheduler/llumlet"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/consts"
)

const DefaultLlumletGrpcTimeoutSeconds = 5

type ReschedulingInterface interface {
	ReschedulingLoop()
}

type ReschedulingPolicy struct {
	c                      *options.SchedulerConfig
	cmsClient              *cms.CMSReadClient
	llumletClientManager   *llumlet.ClientManager
	policies               []reschedulingPolicyInternal
	clusterView            clusterView
	reschedulingIntervalMs int32
	grpcTimeoutSeconds     int
	stopChan               chan bool
}

func NewReschedulingPolicy(config *options.SchedulerConfig) ReschedulingInterface {
	return newReschedulingPolicy(config)
}

func newReschedulingPolicy(c *options.SchedulerConfig) *ReschedulingPolicy {
	cmsClient, err := cms.CreateOrGetClient(
		c.CmsRedisHost,
		c.CmsRedisPort,
		c.CmsRedisUsername,
		c.CmsRedisPassword,
		c.CmsRedisSocketTimeout,
		c.CmsRedisRetryTimes,
		c.CmsPullStatusIntervalMs,
		c.CmsPullMetadataIntervalMs,
		c.AllowConcurrentScheduling,
		c.EnableInstanceStatusLocalAccount,
		c.EnableCacheAwareScheduling,
		c.RequestLocalAccountStalenessSeconds,
		c.CmsRecordMetricsInterval,
		c.EnablePredictorEnhancedScheduling,
		c.NumPredictorWarmupSamples,
		c.EnableAdaptivePD)
	if err != nil {
		panic(err)
	}
	llumletGrpcTimeoutSeconds := c.LlumletGrpcTimeoutSeconds
	if llumletGrpcTimeoutSeconds <= 0 {
		llumletGrpcTimeoutSeconds = DefaultLlumletGrpcTimeoutSeconds
	}
	rp := &ReschedulingPolicy{
		c:         c,
		cmsClient: cmsClient,
		llumletClientManager: llumlet.NewClientManager(
			c.LlumletGrpcConnectionPoolSize,
		),
		clusterView: clusterView{
			groupedInstanceViews: nil,
		},
		grpcTimeoutSeconds:     llumletGrpcTimeoutSeconds,
		reschedulingIntervalMs: c.ReschedulingIntervalMs,
		stopChan:               make(chan bool),
	}
	policies := strings.Split(c.ReschedulingPolicies, ",")
	if c.EnableAdaptivePD {
		if !slices.Contains(policies, consts.ReschedulingPolicyBinPackingConsolidation) {
			policies = append(policies, consts.ReschedulingPolicyBinPackingConsolidation)
		}

		if !slices.Contains(policies, consts.ReschedulingPolicyBinPackingMitigation) {
			policies = append(policies, consts.ReschedulingPolicyBinPackingMitigation)
		}
	}

	for _, policy := range policies {
		rp.policies = append(rp.policies, newReschedulingPolicyInternal(c, policy))
	}
	klog.Infof("Rescheduling initialized, policies: %+v", policies)
	return rp
}

func (p *ReschedulingPolicy) ReschedulingLoop() {
	if p.reschedulingIntervalMs <= 0 {
		klog.Info("reschedulingIntervalMs is less than 0, exiting refreshMetadataLoop")
		return
	}

	klog.Infof("Starting rescheduling loop, IntervalMs: %v", p.reschedulingIntervalMs)
	ticker := time.NewTicker(time.Duration(p.reschedulingIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pairs := p.reschedule()
			p.executeMigrations(pairs)
		case <-p.stopChan:
			return
		}
	}
}

func (p *ReschedulingPolicy) reschedule() []*reschedulingPair {
	if p.c.AllowConcurrentScheduling {
		p.cmsClient.RLock()
		defer p.cmsClient.RUnlock()
	} else {
		p.cmsClient.Lock()
		defer p.cmsClient.Unlock()
	}

	p.clusterView.groupedInstanceViews = toInstanceViewInterfaceMap(p.cmsClient.GetGroupedInstanceViews())
	clusterViewScheduling := toClusterViewScheduling(p.clusterView)
	klog.V(4).Infof("Retrieved cluster instances, count: %d", len(clusterViewScheduling.instanceViews))

	reschedulingPairs := p.getMigrationPairs(clusterViewScheduling.instanceViews)
	klog.V(4).Infof("Generate rescheduling pairs, count: %d", len(reschedulingPairs))
	return reschedulingPairs
}

func (p *ReschedulingPolicy) getMigrationPairs(
	instanceViews map[string]*instanceViewScheduling) (reschedulingPairs []*reschedulingPair) {
	for _, policy := range p.policies {
		policy.calculateMetrics(instanceViews)
		srcInstanceViews, dstInstanceViews := policy.filter(instanceViews)
		selectedPairs := policy.selectPairs(srcInstanceViews, dstInstanceViews)
		reqSelectPolicy := policy.getMigrationReqSelectPolicy()
		reschedulingPairs = p.validateAndAppendPairs(selectedPairs, reqSelectPolicy, reschedulingPairs)
		if len(reschedulingPairs) > 0 {
			klog.V(4).Infof("Generate reschedule pairs for policy %T, pairs: %v", policy, reschedulingPairs)
		}
	}
	return
}

func (p *ReschedulingPolicy) validateAndAppendPairs(
	selectedPairs []*reschedulingPair,
	reqSelectPolicy migrationReqSelectPolicy,
	reschedulingPairs []*reschedulingPair) []*reschedulingPair {

	// make sure pairs in reschedulingPairs is not conflict
	for _, selectPair := range selectedPairs {
		ok := true
		for _, pair := range reschedulingPairs {
			if pair.conflict(selectPair) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		selectPair.reqSelectRule = reqSelectPolicy.rule
		selectPair.reqSelectOrder = reqSelectPolicy.order
		selectPair.reqSelectValue = reqSelectPolicy.value
		reschedulingPairs = append(reschedulingPairs, selectPair)
	}
	return reschedulingPairs
}

type migrationResult struct {
	reschedulingPair *reschedulingPair
	migrateResponse  *llumlet.MigrateResponse
	err              error
}

type migrationReqSelectPolicy struct {
	rule  string
	order string
	value float32
}

func (p *ReschedulingPolicy) executeMigrations(reschedulingPairs []*reschedulingPair) []*migrationResult {
	if len(reschedulingPairs) == 0 {
		return []*migrationResult{}
	}

	results := make(chan migrationResult, len(reschedulingPairs))

	for _, rp := range reschedulingPairs {
		go func(pair *reschedulingPair) {
			labels := metrics.Labels{
				{Name: "rescheduling_req_select_rule", Value: rp.reqSelectRule},
				{Name: "rescheduling_req_select_order", Value: rp.reqSelectOrder},
			}
			metrics.IncrLlumnixCounterByOne(metrics.LlumnixMetricReschedulingCount, labels)
			klog.V(4).Infof("Start to migrate from %s to %s, rule is %s",
				pair.srcView.cmsView.Metadata.Ip+":"+strconv.Itoa(int(pair.srcView.cmsView.Metadata.ApiServerPort)),
				pair.dstView.cmsView.Metadata.Ip+":"+strconv.Itoa(int(pair.dstView.cmsView.Metadata.ApiServerPort)),
				pair.reqSelectRule)
			srcLlumletAddress := rp.srcView.cmsView.Metadata.Ip + ":" + strconv.Itoa(int(rp.srcView.cmsView.Metadata.LlumletPort))
			srcLlumletClientConnection, err :=
				p.llumletClientManager.GetOrCreateClient(rp.srcView.GetInstanceId(), srcLlumletAddress)
			if err != nil {
				klog.Errorf("failed to get llumlet client for instance %s: %v", rp.srcView.GetInstanceId(), err)
				results <- migrationResult{pair, nil, err}
				return
			}
			migrationRequest := &llumlet.MigrateRequest{
				SrcEngineId:        rp.srcView.GetInstanceId(),
				DstEngineId:        rp.dstView.GetInstanceId(),
				DstEngineIp:        rp.dstView.cmsView.Metadata.IpKvs,
				DstEnginePort:      rp.dstView.cmsView.Metadata.KvtPort,
				MigrationType:      rp.reqSelectRule,
				MigrationReqPolicy: rp.reqSelectOrder,
			}
			switch rp.reqSelectRule {
			case consts.MigrationReqSelectRuleNumReq:
				migrationRequest.MigrationValue = &llumlet.MigrateRequest_NumReqs{NumReqs: int64(rp.reqSelectValue)}
			case consts.MigrationReqSelectRuleToken:
				migrationRequest.MigrationValue = &llumlet.MigrateRequest_NumTokens{NumTokens: int64(rp.reqSelectValue)}
			case consts.MigrationReqSelectRuleRatio:
				migrationRequest.MigrationValue = &llumlet.MigrateRequest_KvCacheUsageRatio{KvCacheUsageRatio: rp.reqSelectValue}
			default:
				klog.Errorf("unknown migration type: %s, skip", rp.reqSelectRule)
				err = fmt.Errorf("unknown migration type: %s", rp.reqSelectRule)
				results <- migrationResult{pair, nil, err}
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.grpcTimeoutSeconds)*time.Second)
			defer cancel()
			defer p.llumletClientManager.DecrClientConnectionRequestCnt(rp.srcView.GetInstanceId())
			var migrateResponse *llumlet.MigrateResponse
			migrateResponse, err = srcLlumletClientConnection.LlumletClient.Migrate(ctx, migrationRequest)
			if err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					klog.Errorf("timeout for migrating instance %s: %v", rp.srcView.cmsView.Metadata.InstanceId, err)
				} else {
					klog.Errorf("failed to migrate instance %s: %v", rp.srcView.cmsView.Metadata.InstanceId, err)
				}
			}
			if migrateResponse != nil && !migrateResponse.Success {
				klog.Errorf("failed to migrate from instance %s, to %s: %v",
					rp.srcView.GetInstanceId(), rp.dstView.GetInstanceId(), migrateResponse.Message)
			}
			results <- migrationResult{pair, migrateResponse, err}
			klog.V(4).Infof("Finish migrating from %s to %s, rule is %s",
				pair.srcView.cmsView.Metadata.Ip+":"+strconv.Itoa(int(pair.srcView.cmsView.Metadata.ApiServerPort)),
				pair.dstView.cmsView.Metadata.Ip+":"+strconv.Itoa(int(pair.dstView.cmsView.Metadata.ApiServerPort)),
				pair.reqSelectRule)
		}(rp)
	}

	var migrationResults []*migrationResult
	for i := 0; i < len(reschedulingPairs); i++ {
		result := <-results
		if result.err != nil || !result.migrateResponse.Success {
			metrics.IncrLlumnixCounterByOne(metrics.LlumnixMetricReschedulingFailedCount, metrics.Labels{})
		}
		migrationResults = append(migrationResults, &result)
	}
	close(results)

	return migrationResults
}

type reschedulingPolicyInternal interface {
	// InstanceMetadata is stored at cms, do not modify them
	calculateMetrics(instances map[string]*instanceViewScheduling)
	filter(
		instanceViews map[string]*instanceViewScheduling) (
		src map[string]*instanceViewScheduling,
		dst map[string]*instanceViewScheduling)
	selectPairs(
		srcInstanceView,
		dstInstanceView map[string]*instanceViewScheduling) []*reschedulingPair
	getMigrationReqSelectPolicy() migrationReqSelectPolicy
}

type baseReschedulingPolicy struct {
	metrics                  map[string]func() instanceSchedulingMetric
	srcSingleInstanceFilters []singleInstanceFilter
	srcGlobalFilters         []globalFilter
	dstSingleInstanceFilters []singleInstanceFilter
	dstGlobalFilters         []globalFilter
	selector                 reschedulingSelector
}

func (brp *baseReschedulingPolicy) calculateMetrics(instances map[string]*instanceViewScheduling) {
	calculateMetrics(nil, instances, brp.metrics)
}

func (brp *baseReschedulingPolicy) filter(
	instanceViews map[string]*instanceViewScheduling) (
	map[string]*instanceViewScheduling,
	map[string]*instanceViewScheduling) {

	availableSrcViews := filter(
		instanceViews,
		brp.srcSingleInstanceFilters,
		brp.srcGlobalFilters,
		false)
	availableDstViews := filter(
		instanceViews,
		brp.dstSingleInstanceFilters,
		brp.dstGlobalFilters,
		false)
	return availableSrcViews, availableDstViews
}

func (brp *baseReschedulingPolicy) selectPairs(
	srcInstanceViewInternal,
	dstInstanceViewInternal map[string]*instanceViewScheduling) []*reschedulingPair {
	return brp.selector.selectPairs(srcInstanceViewInternal, dstInstanceViewInternal)
}

func (brp *baseReschedulingPolicy) getMigrationReqSelectPolicy() migrationReqSelectPolicy {
	return migrationReqSelectPolicy{
		rule:  consts.MigrationReqSelectRuleNumReq,
		order: consts.MigrationReqSelectOrderLCR,
		value: 1,
	}
}

type reschedulingPair struct {
	srcView, dstView *instanceViewScheduling
	reqSelectRule    string
	reqSelectOrder   string
	reqSelectValue   float32
}

func (rp *reschedulingPair) equal(rp2 *reschedulingPair) bool {
	return rp.srcView.GetInstanceId() == rp2.srcView.GetInstanceId() &&
		rp.dstView.GetInstanceId() == rp2.dstView.GetInstanceId()
}

func (rp *reschedulingPair) conflict(rp2 *reschedulingPair) bool {
	return rp.srcView.GetInstanceId() == rp2.dstView.GetInstanceId() &&
		rp.dstView.GetInstanceId() == rp2.srcView.GetInstanceId()
}

func (rp *reschedulingPair) String() string {
	return fmt.Sprintf("reschedulePair src: %s, dst: %s", rp.srcView.GetInstanceId(), rp.dstView.GetInstanceId())
}
