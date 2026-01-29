package llumnix

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"llumnix/cmd/llm-gateway/app/options"
	"llumnix/pkg/llm-gateway/cms"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/llumlet"
	"llumnix/pkg/llm-gateway/metrics"
)

const DefaultLlumletGrpcTimeoutSeconds = 5

type ReschedulePolicy struct {
	c                    *options.Config
	cmsClient            *cms.CMSReadClient
	llumletClientManager *llumlet.ClientManager
	policies             []reschedulePolicyInternal
	clusterView          clusterView
	rescheduleIntervalMs int32
	grpcTimeoutSeconds   int
	stopChan             chan bool
}

func NewReschedulePolicy(c *options.Config) *ReschedulePolicy {
	cmsClient, err := cms.CreateOrGetClient(
		c.SchedulerConfig.CmsRedisHost,
		c.SchedulerConfig.CmsRedisPort,
		c.SchedulerConfig.CmsRedisUsername,
		c.SchedulerConfig.CmsRedisPassword,
		c.SchedulerConfig.CmsRedisSocketTimeout,
		c.SchedulerConfig.CmsRedisRetryTimes,
		c.SchedulerConfig.CmsPullStatusIntervalMs,
		c.SchedulerConfig.CmsPullMetadataIntervalMs,
		c.SchedulerConfig.AllowConcurrentSchedule,
		c.SchedulerConfig.EnableInstanceStatusLocalAccount,
		c.SchedulerConfig.EnableCacheAwareScheduling,
		c.SchedulerConfig.RequestLocalAccountStalenessSeconds,
		c.SchedulerConfig.CmsRecordMetricsInterval,
		c.SchedulerConfig.EnablePredictorEnhancedScheduling,
		c.SchedulerConfig.KvCacheBlockSize,
		c.SchedulerConfig.NumPredictorWarmupSamples)
	if err != nil {
		panic(err)
	}
	llumletGrpcTimeoutSeconds := c.SchedulerConfig.LlumletGrpcTimeoutSeconds
	if llumletGrpcTimeoutSeconds <= 0 {
		llumletGrpcTimeoutSeconds = DefaultLlumletGrpcTimeoutSeconds
	}
	rp := &ReschedulePolicy{
		c:         c,
		cmsClient: cmsClient,
		llumletClientManager: llumlet.NewClientManager(
			c.SchedulerConfig.LlumletGrpcConnectionPoolSize,
		),
		clusterView: clusterView{
			groupedInstanceViews: nil,
		},
		grpcTimeoutSeconds:   llumletGrpcTimeoutSeconds,
		rescheduleIntervalMs: c.SchedulerConfig.RescheduleIntervalMs,
		stopChan:             make(chan bool),
	}
	polices := strings.Split(c.SchedulerConfig.ReschedulePolicies, ",")
	if c.SchedulerConfig.EnableAdaptivePD {
		polices = append(polices, consts.ReschedulePolicyCleanUpDecodeRequestsOnPrefill)
		polices = append(polices, consts.ReschedulePolicyAggregateDecodeRequestsOnPrefill)
		polices = append(polices, consts.ReschedulePolicyEaseBusyDecodeWithFreePrefill)
	}
	for _, policy := range polices {
		rp.policies = append(rp.policies, newReschedulePolicyInternal(&c.SchedulerConfig, policy))
	}
	klog.Infof("Reschedule initialized, policies: %+v", polices)
	return rp
}

func (p *ReschedulePolicy) RescheduleLoop() {
	if p.rescheduleIntervalMs <= 0 {
		klog.Info("rescheduleIntervalMs is less than 0, exiting refreshMetadataLoop")
		return
	}

	klog.Infof("Starting reschedule loop, IntervalMs: %v", p.rescheduleIntervalMs)
	ticker := time.NewTicker(time.Duration(p.rescheduleIntervalMs) * time.Millisecond)
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

func (p *ReschedulePolicy) reschedule() []*reschedulePair {
	if p.c.SchedulerConfig.AllowConcurrentSchedule {
		p.cmsClient.RLock()
		defer p.cmsClient.RUnlock()
	} else {
		p.cmsClient.Lock()
		defer p.cmsClient.Unlock()
	}

	p.clusterView.groupedInstanceViews = toInstanceViewInterfaceMap(p.cmsClient.GetGroupedInstanceViews())
	clusterViewScheduling := toClusterViewScheduling(p.clusterView)
	klog.V(4).Infof("Retrieved cluster instances, count: %d", len(clusterViewScheduling.instanceViews))

	reschedulePairs := p.getMigrationPairs(clusterViewScheduling.instanceViews)
	klog.V(4).Infof("Generate reschedule pairs, count: %d", len(reschedulePairs))
	return reschedulePairs
}

func (p *ReschedulePolicy) getMigrationPairs(
	instanceViews map[string]*instanceViewScheduling) (reschedulePairs []*reschedulePair) {
	for _, policy := range p.policies {
		policy.calculateMetrics(instanceViews)
		srcInstanceViews, dstInstanceViews := policy.filter(instanceViews)
		selectedPairs := policy.selectPairs(srcInstanceViews, dstInstanceViews)
		reqSelectPolicy := policy.getMigrationReqSelectPolicy()
		reschedulePairs = p.validateAndAppendPairs(selectedPairs, reqSelectPolicy, reschedulePairs)
	}
	return
}

func (p *ReschedulePolicy) validateAndAppendPairs(
	selectedPairs []*reschedulePair,
	reqSelectPolicy migrationReqSelectPolicy,
	reschedulePairs []*reschedulePair) []*reschedulePair {

	// make sure pairs in reschedulePairs is not conflict
	for _, selectPair := range selectedPairs {
		ok := true
		for _, pair := range reschedulePairs {
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
		reschedulePairs = append(reschedulePairs, selectPair)
	}
	return reschedulePairs
}

type migrationResult struct {
	reschedulePair  *reschedulePair
	migrateResponse *llumlet.MigrateResponse
	err             error
}

type migrationReqSelectPolicy struct {
	rule  string
	order string
	value float32
}

func (p *ReschedulePolicy) executeMigrations(reschedulePairs []*reschedulePair) []*migrationResult {
	if len(reschedulePairs) == 0 {
		return []*migrationResult{}
	}

	results := make(chan migrationResult, len(reschedulePairs))

	for _, rp := range reschedulePairs {
		go func(pair *reschedulePair) {
			labels := metrics.Labels{
				{Name: "reschedule_req_select_rule", Value: rp.reqSelectRule},
				{Name: "reschedule_req_select_order", Value: rp.reqSelectOrder},
			}
			metrics.IncrLlumnixCounterByOne(metrics.LlumnixMetricRescheduleCount, labels)
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
				migrationRequest.MigrationValue = &llumlet.MigrateRequest_BlockRatio{BlockRatio: rp.reqSelectValue}
			default:
				klog.Errorf("unknown migration type: %s, skip", rp.reqSelectRule)
				err = fmt.Errorf("unknown migration type: %s", rp.reqSelectRule)
				results <- migrationResult{pair, nil, err}
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.grpcTimeoutSeconds)*time.Second)
			defer cancel()
			defer p.llumletClientManager.DecrClientConnectionRequestCnt(rp.srcView.GetInstanceId())
			migrateResponse := &llumlet.MigrateResponse{}
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
	for i := 0; i < len(reschedulePairs); i++ {
		result := <-results
		if result.err != nil || !result.migrateResponse.Success {
			metrics.IncrLlumnixCounterByOne(metrics.LlumnixMetricRescheduleFailedCount, metrics.Labels{})
		}
		migrationResults = append(migrationResults, &result)
	}
	close(results)

	return migrationResults
}

type reschedulePolicyInternal interface {
	// InstanceMetadata is stored at cms, do not modify them
	calculateMetrics(instances map[string]*instanceViewScheduling)
	filter(
		instanceViews map[string]*instanceViewScheduling) (
		src map[string]*instanceViewScheduling,
		dst map[string]*instanceViewScheduling)
	selectPairs(
		srcInstanceView,
		dstInstanceView map[string]*instanceViewScheduling) []*reschedulePair
	getMigrationReqSelectPolicy() migrationReqSelectPolicy
}

type baseReschedulePolicy struct {
	metrics                  map[string]func() instanceSchedulingMetric
	srcSingleInstanceFilters []singleInstanceFilter
	srcGlobalFilters         []globalFilter
	dstSingleInstanceFilters []singleInstanceFilter
	dstGlobalFilters         []globalFilter
	selector                 rescheduleSelector
}

func (brp *baseReschedulePolicy) calculateMetrics(instances map[string]*instanceViewScheduling) {
	calculateMetrics(instances, brp.metrics)
}

func (brp *baseReschedulePolicy) filter(
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

func (brp *baseReschedulePolicy) selectPairs(
	srcInstanceViewInternal,
	dstInstanceViewInternal map[string]*instanceViewScheduling) []*reschedulePair {
	return brp.selector.selectPairs(srcInstanceViewInternal, dstInstanceViewInternal)
}

func (brp *baseReschedulePolicy) getMigrationReqSelectPolicy() migrationReqSelectPolicy {
	return migrationReqSelectPolicy{
		rule:  consts.MigrationReqSelectRuleNumReq,
		order: consts.MigrationReqSelectOrderLCR,
		value: 1,
	}
}

type reschedulePair struct {
	srcView, dstView *instanceViewScheduling
	reqSelectRule    string
	reqSelectOrder   string
	reqSelectValue   float32
}

func (rp *reschedulePair) equal(rp2 *reschedulePair) bool {
	return rp.srcView.GetInstanceId() == rp2.srcView.GetInstanceId() &&
		rp.dstView.GetInstanceId() == rp2.dstView.GetInstanceId()
}

func (rp *reschedulePair) conflict(rp2 *reschedulePair) bool {
	return rp.srcView.GetInstanceId() == rp2.dstView.GetInstanceId() &&
		rp.dstView.GetInstanceId() == rp2.srcView.GetInstanceId()
}
