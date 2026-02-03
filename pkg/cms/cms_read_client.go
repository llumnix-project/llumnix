package cms

import (
	"fmt"
	"llumnix/pkg/metrics"
	"llumnix/pkg/scheduler/predictor"
	"math"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
	"llumnix/pkg/utils"
)

const (
	LOG_INTERVAL_S = 10
)

var (
	client *CMSReadClient
	mu     sync.RWMutex // to protect client
)

type InstanceView struct {
	Instance *types.LLMInstance
	Status   *InstanceStatus
	Metadata *InstanceMetadata
	InstanceStatusLocalAccount
}

func (iv *InstanceView) GetInstance() *types.LLMInstance {
	return iv.Instance
}

func (iv *InstanceView) GetInstanceId() string {
	return iv.Metadata.InstanceId
}

func (iv *InstanceView) GetInferMode() string {
	return iv.Instance.Role.String()
}

type CMSReadClientInterface interface {
	RLock()

	RUnlock()

	Lock()

	Unlock()

	// GetInstanceIDs returns all instance IDs
	GetInstanceIDs() []string

	// GetInstanceMetadatas returns all instance metadata
	GetInstanceMetadatas() map[string]*InstanceMetadata

	// GetInstanceMetadataByID returns instance metadata by ID
	GetInstanceMetadataByID(instanceID string) *InstanceMetadata

	// GetInstanceStatuses returns all instance statuses
	GetInstanceStatuses() map[string]*InstanceStatus

	// GetInstanceStatusByID returns instance status by ID
	GetInstanceStatusByID(instanceID string) *InstanceStatus

	// GetInstanceIDsByIPs get instance id set of given ips
	GetInstanceIDsByIPs(ips []string) sets.String

	// GetInstanceViews returns all instance views
	GetInstanceViews() map[string]*InstanceView

	// GetGroupedInstanceViews returns all instance views grouped by infer mode
	GetGroupedInstanceViews() map[string]map[string]*InstanceView
}

// CMSReadClient provides CMS read operation interfaces
type CMSReadClient struct {
	redisClient RedisClientInterface

	// BE CAREFUL when reading the data from outside CMSReadClient, e.g., from the scheduling policy.
	// You MUST call cms.RLock() and defer cms.RUnlock() before reading the data.
	instanceIDs           []string
	instanceIDsSet        sets.String
	instanceMetadatas     map[string]*InstanceMetadata
	instanceStatuses      map[string]*InstanceStatus
	instanceViews         map[string]*InstanceView
	groupedInstanceViews  map[string]map[string]*InstanceView
	statusUnmarshalBuffer InstanceStatus
	ipToInstanceIDs       map[string]sets.String
	instanceIDToIP        map[string]string

	mu       sync.RWMutex
	stopChan chan bool
	isAlive  bool

	redisPullStatusIntervalMs   int32
	redisPullMetadataIntervalMs int32

	allowConcurrentSchedule bool

	enableInstanceStatusLocalAccount bool
	instanceStatusLocalAccountEditor *InstanceStatusLocalAccountEditor

	enableCacheAwareScheduling bool

	recordMetricsInterval int32

	enablePredictorEnhancedScheduling bool
	TTFTPredictor                     *predictor.QuadraticPredictor
}

func (c *CMSReadClient) IsAlive() bool {
	return c.isAlive
}

func CreateOrGetClient(
	host string,
	port string,
	username string,
	password string,
	socketTimeout float64,
	retryTimes int,
	pullStatusIntervalMs int32,
	pullMetadataIntervalMs int32,
	allowConcurrentSchedule bool,
	enableInstanceStatusLocalAccount bool,
	enableCacheAwareScheduling bool,
	requestLocalAccountStalenessSeconds int32,
	recordMetricsInterval int32,
	enablePredictorEnhancedScheduling bool,
	numPredictorWarmupSamples int) (*CMSReadClient, error) {

	mu.Lock()
	defer mu.Unlock()

	if client != nil {
		return client, nil
	}

	redisClient, err := NewRedisClient(host, port, username, password, socketTimeout, retryTimes)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	client, err = NewCMSReadClient(
		redisClient, pullStatusIntervalMs, pullMetadataIntervalMs, allowConcurrentSchedule, enableInstanceStatusLocalAccount,
		enableCacheAwareScheduling, requestLocalAccountStalenessSeconds, recordMetricsInterval,
		enablePredictorEnhancedScheduling, numPredictorWarmupSamples)

	return client, err
}

func NewCMSReadClient(
	redisClient RedisClientInterface,
	pullStatusIntervalMs int32,
	pullMetadataIntervalMs int32,
	allowConcurrentSchedule bool,
	enableInstanceStatusLocalAccount bool,
	enableCacheAwareScheduling bool,
	requestLocalAccountStalenessSeconds int32,
	recordMetricsInterval int32,
	enablePredictorEnhancedScheduling bool,
	numPredictorWarmupSamples int) (*CMSReadClient, error) {

	if redisClient == nil {
		return nil, fmt.Errorf("CMS redis client cannot be nil")
	}

	client := &CMSReadClient{
		redisClient:                       redisClient,
		instanceIDsSet:                    sets.NewString(),
		instanceMetadatas:                 make(map[string]*InstanceMetadata),
		instanceStatuses:                  make(map[string]*InstanceStatus),
		instanceViews:                     make(map[string]*InstanceView),
		groupedInstanceViews:              make(map[string]map[string]*InstanceView),
		statusUnmarshalBuffer:             InstanceStatus{},
		instanceIDToIP:                    make(map[string]string),
		ipToInstanceIDs:                   make(map[string]sets.String),
		stopChan:                          make(chan bool),
		redisPullStatusIntervalMs:         pullStatusIntervalMs,
		redisPullMetadataIntervalMs:       pullMetadataIntervalMs,
		allowConcurrentSchedule:           allowConcurrentSchedule,
		enableCacheAwareScheduling:        enableCacheAwareScheduling,
		enableInstanceStatusLocalAccount:  enableInstanceStatusLocalAccount,
		recordMetricsInterval:             recordMetricsInterval,
		enablePredictorEnhancedScheduling: enablePredictorEnhancedScheduling,
	}

	if enableInstanceStatusLocalAccount {
		client.instanceStatusLocalAccountEditor = newInstanceStatusLocalAccountEditor(
			requestLocalAccountStalenessSeconds, enableCacheAwareScheduling)
	}

	if enablePredictorEnhancedScheduling {
		client.TTFTPredictor = predictor.NewQuadraticPredictor(numPredictorWarmupSamples)
	}

	go client.refreshMetadataLoop()
	go client.refreshStatusLoop()
	klog.Info("CMSReadClient initialized")
	return client, nil
}

func (c *CMSReadClient) RLock() {
	c.mu.RLock()
}

func (c *CMSReadClient) RUnlock() {
	c.mu.RUnlock()
}

func (c *CMSReadClient) Lock() {
	c.mu.Lock()
}

func (c *CMSReadClient) Unlock() {
	c.mu.Unlock()
}

// refreshMetadataLoop periodically fetches metadata from Redis and updates the local cache
func (c *CMSReadClient) refreshMetadataLoop() {
	if c.redisPullMetadataIntervalMs <= 0 {
		klog.Warning("[refreshMetadataLoop] redisPullMetadataIntervalMs is less than 0, exiting refreshMetadataLoop")
		return
	}
	metricRecordIndex := int32(0)
	lastLogTime := time.Now()
	for {
		start := time.Now()
		c.refreshInstanceMetadata()
		elapsed := time.Since(start)
		if time.Since(lastLogTime) > LOG_INTERVAL_S*time.Second {
			klog.V(3).Infof("[refreshMetadataLoop] refreshInstanceMetadata took %v", elapsed)
			lastLogTime = time.Now()
		}
		if c.recordMetricsInterval > 0 && metricRecordIndex == 0 {
			metrics.AddLlumnixLatency(metrics.LlumnixMetricCmsRefreshInstanceMetadataLatencyMicroseconds, metrics.Labels{}, elapsed.Microseconds())
			metricRecordIndex = (metricRecordIndex + 1) % c.recordMetricsInterval
		}
		sleepTime := time.Duration(c.redisPullMetadataIntervalMs)*time.Millisecond - elapsed
		if sleepTime < 0 {
			sleepTime = 0
		}

		select {
		case <-time.After(sleepTime):
		case <-c.stopChan:
			return
		}
	}
}

func (c *CMSReadClient) refreshStatusLoop() {
	if c.redisPullStatusIntervalMs <= 0 {
		klog.Warning("[refreshStatusLoop] redisPullStatusIntervalMs is less than 0, exiting refreshStatusLoop")
		return
	}
	metricRecordIndex := int32(0)
	lastLogTime := time.Now()
	for {
		start := time.Now()
		needRecordMetrics := false
		if c.recordMetricsInterval > 0 {
			needRecordMetrics = metricRecordIndex == 0
			metricRecordIndex = (metricRecordIndex + 1) % c.recordMetricsInterval
		}
		c.refreshInstanceStatus(needRecordMetrics)
		elapsed := time.Since(start)
		if time.Since(lastLogTime) > LOG_INTERVAL_S*time.Second {
			klog.V(4).Infof("[refreshStatusLoop] refreshInstanceStatus took %v", elapsed)
			lastLogTime = time.Now()
		}
		if needRecordMetrics {
			metrics.AddLlumnixLatency(metrics.LlumnixMetricCmsRefreshInstanceStatusLatencyMicroseconds, metrics.Labels{}, elapsed.Microseconds())
		}

		sleepTime := time.Duration(c.redisPullStatusIntervalMs)*time.Millisecond - elapsed
		if sleepTime < 0 {
			sleepTime = 0
		}

		select {
		case <-time.After(sleepTime):
		case <-c.stopChan:
			return
		}
	}
}

// gets instance metadata from Redis and refreshes local cache
func (c *CMSReadClient) refreshInstanceMetadata() {
	defer func() {
		if r := recover(); r != nil {
			klog.Errorf("[refreshInstanceMetadata] Error refreshing instance metadata from Redis: %v", r)
		}
	}()

	// get instance ids
	instanceIDsInRedis, err := c.redisClient.GetKeysByPrefix(LlumnixInstanceMetadataPrefix)
	if err != nil {
		c.Lock()
		defer c.Unlock()
		c.isAlive = false
		klog.Errorf("[refreshInstanceMetadata] Error getting keys by prefix: %v", err)
		return
	}

	instanceIDsNew := make([]string, 0, len(instanceIDsInRedis))
	instanceIDsSet := sets.NewString()
	for _, id := range instanceIDsInRedis {
		instanceID := strings.TrimPrefix(id, LlumnixInstanceMetadataPrefix)
		instanceIDsNew = append(instanceIDsNew, instanceID)
		instanceIDsSet.Insert(instanceID)
	}

	// get instance metadata
	instanceMetadataBytesList, err := c.redisClient.MGetBytes(instanceIDsInRedis)
	c.Lock()
	defer c.Unlock()
	if err != nil {
		c.isAlive = false
		klog.Errorf("[refreshInstanceMetadata] Error getting metadata: %v", err)
		return
	}

	c.isAlive = true
	// update instance ids
	c.instanceIDs = instanceIDsNew
	c.instanceIDsSet = instanceIDsSet

	// Clean up metadata dict by removing instances that no longer exist
	for instanceID, instanceMeta := range c.instanceMetadatas {
		if !c.instanceIDsSet.Has(instanceID) {
			if oldIP := c.instanceIDToIP[instanceID]; oldIP != "" {
				c.removeFromIp2InstanceIDsMap(oldIP, instanceID)
			} else if instanceMeta != nil {
				if oldIP := instanceMeta.GetIpKvs(); oldIP != "" {
					c.removeFromIp2InstanceIDsMap(oldIP, instanceID)
				}
			}
			delete(c.instanceMetadatas, instanceID)
			delete(c.instanceIDToIP, instanceID)
			klog.Warningf(
				"[refreshInstanceMetadata] Instance ID %s does not exist in instanceIDsSet.", instanceID)
		}
	}

	// update instance metadata
	for i, instanceMetadataBytes := range instanceMetadataBytesList {
		instanceID := instanceIDsNew[i]

		if instanceMetadataBytes == nil {
			c.instanceMetadatas[instanceID] = nil
			klog.Warningf(
				"[refreshInstanceMetadata] InstanceMetadataBytes is nil, instanceID=%s", instanceID)
			continue // Value does not exist
		}

		// get old ip
		var oldIP string
		if meta, ok := c.instanceMetadatas[instanceID]; ok && meta != nil {
			oldIP = meta.GetIpKvs()
		}

		if metadata, exists := c.instanceMetadatas[instanceID]; !exists || metadata == nil {
			c.instanceMetadatas[instanceID] = &InstanceMetadata{}
			klog.V(4).Infof("[refreshInstanceMetadata] instanceID=%s, metadata=%+v",
				instanceID, c.instanceMetadatas[instanceID])
		}
		instanceMetadata := c.instanceMetadatas[instanceID]

		// unmarshal: all-or-nothing
		if err := proto.Unmarshal(instanceMetadataBytes, instanceMetadata); err != nil {
			klog.Errorf("[refreshInstanceMetadata] Error unmarshalling metadata: %v, instanceID=%s",
				err, instanceID)
			continue
		}

		newIP := instanceMetadata.GetIpKvs()
		if newIP != oldIP {
			if oldIP != "" {
				c.removeFromIp2InstanceIDsMap(oldIP, instanceID)
			}
			if newIP != "" {
				klog.V(4).Infof(
					"[refreshInstanceMetadata] instanceID=%s, ipKvs='%s'",
					instanceID, instanceMetadata.GetIpKvs())
				c.addToIpToInstanceIDsMap(newIP, instanceID)
			}
			if newIP == "" {
				delete(c.instanceIDToIP, instanceID)
			} else {
				c.instanceIDToIP[instanceID] = newIP
			}
		} else {
			if newIP == "" {
				delete(c.instanceIDToIP, instanceID)
			}
		}
	}
}

// addToIpToInstanceIDsMap add instance id to ip to instance id set map
func (c *CMSReadClient) addToIpToInstanceIDsMap(ip, instanceID string) {
	klog.V(4).Infof("[addToIpToInstanceIDsMap] ip='%s', instanceID='%s'", ip, instanceID)
	if ip == "" {
		klog.Warningf("[addToIpToInstanceIDsMap] ip is empty, skipping, instanceID=%s", instanceID)
		return
	}
	if instanceIDs, exists := c.ipToInstanceIDs[ip]; exists {
		instanceIDs.Insert(instanceID)
	} else {
		c.ipToInstanceIDs[ip] = sets.NewString(instanceID)
	}
}

// removeFromIp2InstanceIDsMap remove instance id from ip to instance id set map
func (c *CMSReadClient) removeFromIp2InstanceIDsMap(ip, instanceID string) {
	if ip == "" {
		return
	}
	if instanceIDs, exists := c.ipToInstanceIDs[ip]; exists {
		klog.V(4).Infof("[removeFromIp2InstanceIDsMap] ip='%s', instanceID='%s'", ip, instanceID)
		instanceIDs.Delete(instanceID)
		if instanceIDs.Len() == 0 {
			delete(c.ipToInstanceIDs, ip)
		}
	}
}

// refreshInstanceStatus gets instance status from Redis and refreshes local cache
func (c *CMSReadClient) refreshInstanceStatus(needRecordMetrics bool) {
	defer func() {
		if r := recover(); r != nil {
			klog.Errorf("[refreshInstanceStatus] Error refreshing data from Redis: %v", r)
		}
	}()
	// update instance status
	c.Lock()
	defer c.Unlock()
	redisKeys := make([]string, len(c.instanceIDs))
	for i, instanceID := range c.instanceIDs {
		redisKeys[i] = LlumnixInstanceStatusPrefix + instanceID
	}
	instanceStatusBytesList, err := c.redisClient.MGetBytes(redisKeys)
	if err != nil {
		c.isAlive = false
		klog.Errorf("[refreshInstanceStatus] Error getting status: %v", err)
		return
	}
	c.isAlive = true

	// Clean up status dict by removing instances that no longer exist
	for instanceID := range c.instanceStatuses {
		if !c.instanceIDsSet.Has(instanceID) {
			delete(c.instanceStatuses, instanceID)
			delete(c.instanceViews, instanceID)
			for inferMode, instanceViews := range c.groupedInstanceViews {
				delete(instanceViews, instanceID)
				if len(instanceViews) == 0 {
					delete(c.groupedInstanceViews, inferMode)
				}
			}
		}
	}
	// update status
	for i, instanceStatusBytes := range instanceStatusBytesList {
		instanceID := c.instanceIDs[i]
		if instanceStatusBytes == nil {
			delete(c.instanceStatuses, instanceID)
			delete(c.instanceViews, instanceID)
			for inferMode, instanceViews := range c.groupedInstanceViews {
				delete(instanceViews, instanceID)
				if len(instanceViews) == 0 {
					delete(c.groupedInstanceViews, inferMode)
				}
			}
			continue // status does not exist
		}
		if _, exists := c.instanceMetadatas[instanceID]; !exists {
			continue
		}
		if status, exists := c.instanceStatuses[instanceID]; !exists || status == nil {
			c.instanceStatuses[instanceID] = &InstanceStatus{
				StepId: math.MinInt32, UpdateId: math.MinInt32, TimestampMs: math.MinInt64,
				ProfilingId: math.MinInt32,
			}
			instanceRole := utils.TransformInstanceType2InferMode(c.instanceMetadatas[instanceID].InstanceType)
			c.instanceViews[instanceID] = &InstanceView{
				Metadata: c.instanceMetadatas[instanceID],
				Status:   c.instanceStatuses[instanceID],
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{
						Host: c.instanceMetadatas[instanceID].Ip,
						Port: int(c.instanceMetadatas[instanceID].ApiServerPort),
					},
					AuxIp:   c.instanceMetadatas[instanceID].IpKvt,
					AuxPort: int(c.instanceMetadatas[instanceID].KvtPort),
					Role:    types.InferRole(instanceRole),
					DPRank:  int(c.instanceMetadatas[instanceID].DpRank),
					DPSize:  int(c.instanceMetadatas[instanceID].DataParallelSize),
					// NOTE(zhaohanyu.zhy): use v6d parser format by default
					ID: fmt.Sprintf("%s_instance%d_%d",
						c.instanceMetadatas[instanceID].Ip,
						c.instanceMetadatas[instanceID].DpRank,
						c.instanceMetadatas[instanceID].DataParallelSize),
				},
				InstanceStatusLocalAccount: InstanceStatusLocalAccount{
					RequestLocalAccount:                                map[string]*RequestLocalAccount{},
					NumInflightDispatchPrefillRequests:                 0,
					NumInflightDispatchDecodeRequests:                  0,
					NumInflightDispatchRequests:                        0,
					NumUncomputedTokensInflightDispatchPrefillRequests: 0,
					NumTokensInflightDispatchDecodeRequests:            0,
				},
			}
			if c.groupedInstanceViews[instanceRole] == nil {
				c.groupedInstanceViews[instanceRole] = make(map[string]*InstanceView)
			}
			c.groupedInstanceViews[instanceRole][instanceID] = c.instanceViews[instanceID]
		}
		err := proto.Unmarshal(instanceStatusBytes, &c.statusUnmarshalBuffer)
		if err != nil {
			klog.Errorf("[refreshInstanceStatus] Error unmarshalling status: %v, instanceID=%s", err, instanceID)
		}

		updateInstanceStatus := true
		updateTTFTPredictor := true
		if c.statusUnmarshalBuffer.TimestampMs <= c.instanceStatuses[instanceID].TimestampMs {
			klog.V(5).Infof("[refreshInstanceStatus] Not newer timestamp, skip update instance status, "+
				"instanceID=%s", instanceID)
			continue
		}
		if c.statusUnmarshalBuffer.UpdateId <= c.instanceStatuses[instanceID].UpdateId {
			klog.V(5).Infof("[refreshInstanceStatus] Not newer update id, skip update instance status, "+
				"instanceID=%s", instanceID)
			updateInstanceStatus = false
		}
		if c.statusUnmarshalBuffer.ProfilingId == -1 ||
			c.statusUnmarshalBuffer.ProfilingId <= c.instanceStatuses[instanceID].ProfilingId {
			klog.V(5).Infof("[refreshInstanceStatus] Not newer profiling id, skip update ttft predictor, "+
				"instanceID=%s", instanceID)
			updateTTFTPredictor = false
		}

		if updateInstanceStatus {
			klog.V(4).Infof("[refreshInstanceStatus] Update instance status, instanceID=%s", instanceID)
			proto.Reset(c.instanceStatuses[instanceID])
			proto.Merge(c.instanceStatuses[instanceID], &c.statusUnmarshalBuffer)
			proto.Reset(&c.statusUnmarshalBuffer)
		} else {
			// Only update timestamp when stepId id is not newer.
			c.instanceStatuses[instanceID].TimestampMs = c.statusUnmarshalBuffer.TimestampMs
			c.instanceStatuses[instanceID].Schedulable = c.statusUnmarshalBuffer.Schedulable
			proto.Reset(&c.statusUnmarshalBuffer)
		}

		if c.enableInstanceStatusLocalAccount && updateInstanceStatus {
			c.instanceStatusLocalAccountEditor.updateInstanceStatusLocalAccount(c.instanceViews[instanceID], instanceID)
		}

		if c.enablePredictorEnhancedScheduling && updateTTFTPredictor {
			c.addSampleToTTFTPredictor(instanceID)
		}

		if needRecordMetrics {
			metrics.SetLlumnixStatusValue(metrics.LlumnixMetricInstanceNumUncomputedTokensAllWaitingPrefills,
				metrics.Labels{{"instance_id", c.instanceStatuses[instanceID].InstanceId}},
				float32(c.instanceStatuses[instanceID].NumUncomputedTokensAllWaitingPrefills))
			metrics.SetLlumnixStatusValue(metrics.LlumnixMetricInstanceNumUsedGpuTokens,
				metrics.Labels{{"instance_id", c.instanceStatuses[instanceID].InstanceId}},
				float32(c.instanceStatuses[instanceID].NumUsedGpuTokens))
		}
	}

	if c.enablePredictorEnhancedScheduling && !c.TTFTPredictor.Fitted() {
		c.fitTTFTPredictor()
	}
}

func (c *CMSReadClient) AddRequestLocalAccount(
	instanceInfo *InstanceView, inferMode string, numTokens int32, prefixHitNumTokens int32, requestId string, firstUpdate bool) {
	if c.allowConcurrentSchedule {
		c.RUnlock()
		defer c.RLock()
		c.Lock()
		defer c.Unlock()
	}
	c.instanceStatusLocalAccountEditor.addRequestLocalAccount(
		instanceInfo, inferMode, numTokens, prefixHitNumTokens, requestId, firstUpdate)
}

func (c *CMSReadClient) RevertRequestPrefillLocalAccount(
	instanceInfo *InstanceView, numTokens int32, prefixHitNumTokens int32, requestId string) {
	if c.allowConcurrentSchedule {
		c.RUnlock()
		defer c.RLock()
		c.Lock()
		defer c.Unlock()
	}
	c.instanceStatusLocalAccountEditor.revertRequestPrefillLocalAccount(
		instanceInfo, numTokens, prefixHitNumTokens, requestId)
}

func (c *CMSReadClient) addSampleToTTFTPredictor(instanceID string) {
	if c.instanceViews[instanceID].GetInferMode() != consts.PrefillInferMode {
		return
	}
	if c.instanceViews[instanceID].Status.NumScheduledPrefillTokens == 0 ||
		c.instanceViews[instanceID].Status.StepDuration <= 0.0 {
		klog.Warningf(
			"[addSampleToTTFTPredictor] illegal data, (NumScheduledPrefillTokens: %v, StepDuration: %v), instanceID=%s",
			c.instanceViews[instanceID].Status.NumScheduledPrefillTokens,
			c.instanceViews[instanceID].Status.StepDuration,
			instanceID)
		return
	}
	c.TTFTPredictor.AddSample(
		float64(c.instanceViews[instanceID].Status.NumScheduledPrefillTokens), c.instanceViews[instanceID].Status.StepDuration)
}

func (c *CMSReadClient) fitTTFTPredictor() {
	// need at least 3 points for quadratic fit
	if !c.TTFTPredictor.ReadyForFit() {
		return
	}
	if err := c.TTFTPredictor.Fit(); err != nil {
		klog.Warningf("[fitTTFTPredictor] failed to fit ttft predictor: %v", err)
	}
}

func (c *CMSReadClient) GetInstanceIDs() []string {
	return c.instanceIDs
}

func (c *CMSReadClient) GetInstanceMetadatas() map[string]*InstanceMetadata {
	return c.instanceMetadatas
}

func (c *CMSReadClient) GetInstanceMetadataByID(instanceID string) *InstanceMetadata {
	if metadata, exists := c.instanceMetadatas[instanceID]; exists && metadata != nil {
		return metadata
	}
	return nil
}

func (c *CMSReadClient) GetInstanceStatuses() map[string]*InstanceStatus {
	return c.instanceStatuses
}

func (c *CMSReadClient) GetInstanceStatusByID(instanceID string) *InstanceStatus {
	if status, exists := c.instanceStatuses[instanceID]; exists && status != nil {
		return status
	}
	return nil
}

func (c *CMSReadClient) GetInstanceIDsByIPs(ips []string) sets.String {
	instanceIDs := sets.NewString()
	klog.V(5).Infof("[GetInstanceIDsByIPs] ipToInstanceIDs=%+v", c.ipToInstanceIDs)
	for _, ip := range ips {
		if ids, ok := c.ipToInstanceIDs[ip]; ok {
			instanceIDs = instanceIDs.Union(ids)
		}
	}
	klog.V(5).Infof("[GetInstanceIDsByIPs] instanceIDs=%s", instanceIDs)
	return instanceIDs
}

func (c *CMSReadClient) GetInstanceViews() map[string]*InstanceView {
	return c.instanceViews
}

func (c *CMSReadClient) GetGroupedInstanceViews() map[string]map[string]*InstanceView {
	return c.groupedInstanceViews
}

// Close stops the refresh loop
func (c *CMSReadClient) Close() {
	close(c.stopChan)
}
