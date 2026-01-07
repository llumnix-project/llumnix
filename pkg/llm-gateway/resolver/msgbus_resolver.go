package resolver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
)

const DefaultParserName = "default"

type MsgKV map[string]string
type MsgKVs []MsgKV

type LLMWorkerParser interface {
	ParseWorker(key, value string) (*structs.LLMWorker, error)
	CheckInstance(instance *structs.LLMInstance) (bool, error)
	ParseDPRankSize(workerId string) (int, int, error)
}

var parserMap map[string]LLMWorkerParser

func init() {
	parserMap = make(map[string]LLMWorkerParser)
	parserMap[DefaultParserName] = &v6dParser{}
}

func GetParser(splitMode string) LLMWorkerParser {
	if parser, ok := parserMap[splitMode]; ok {
		return parser
	} else {
		return parserMap[DefaultParserName]
	}
}

type v6dParser struct {
	// - @Description: The information format of message bus:
	//
	// {
	// 	"__instance__": "ep-inference-5380-decode-ep32tp1dp32-91e7d-2",
	// 	"__timestamp__": "1751306504",
	// 	"__version__": "1",
	// 	"worker0_8": "decode,11.224.50.49,8000,9000",
	// 	"worker1_8": "decode,11.224.50.49,8001,9001",
	// 	"worker2_8": "decode,11.224.50.49,8002,9002",
	// 	"worker3_8": "decode,11.224.50.49,8003,9003",
	// 	"worker4_8": "decode,11.224.50.49,8004,9004",
	// 	"worker5_8": "decode,11.224.50.49,8005,9005",
	// 	"worker6_8": "decode,11.224.50.49,8006,9006",
	// 	"worker7_8": "decode,11.224.50.49,8007,9007"
	//   },
}
type WorkerInfo struct {
	Ip         string
	Port       int
	OptionPort int
	InferMode  string
}

func (p *v6dParser) ParseWorker(key, value string) (*structs.LLMWorker, error) {
	pattern := `^worker\d+_\d+$`
	matched, err := regexp.MatchString(pattern, key)
	if err != nil {
		return nil, nil
	}
	if !matched {
		return nil, nil
	}

	workerInfo := p.valueParse(value)
	worker := &structs.LLMWorker{
		WorkerId:  fmt.Sprintf("%s_%s", workerInfo.Ip, key),
		InferMode: workerInfo.InferMode,
		// each worker is a single dp rank, tp rank won't registered
		TPRank: 0,
		Ep: structs.Endpoint{
			IP:         workerInfo.Ip,
			Port:       workerInfo.Port,
			OptionPort: workerInfo.OptionPort,
		},
	}
	return worker, nil
}

func (p *v6dParser) CheckInstance(instance *structs.LLMInstance) (bool, error) {
	if len(instance.Workers) <= 0 {
		return true, nil
	}
	inferMode := instance.Workers[0].InferMode
	for _, worker := range instance.Workers {
		if worker.InferMode != inferMode {
			return false, fmt.Errorf("infer mode not match")
		}
	}
	instance.InferMode = inferMode
	return true, nil
}

func (p *v6dParser) ParseDPRankSize(workerId string) (int, int, error) {
	strs := strings.Split(workerId, "_")
	if len(strs) < 3 {
		return 0, 1, fmt.Errorf("invalid worker id")
	}
	dpRank, err := strconv.Atoi(strings.TrimPrefix(strs[1], "worker"))
	if err != nil {
		return 0, 1, fmt.Errorf("invalid worker id")
	}
	dpSize, err := strconv.Atoi(strs[2])
	if err != nil {
		return 0, 1, fmt.Errorf("invalid worker id")
	}
	return dpRank, dpSize, nil
}

// legacy parser to workerInfo of v6d format
func (p *v6dParser) valueParse(str string) (workerInfo WorkerInfo) {
	fields := strings.Split(str, ",")
	if len(fields) < 3 {
		klog.Errorf("valueParse failed, got %v", str)
		return
	}

	workerInfo.InferMode = fields[0]
	workerInfo.Ip = fields[1]
	tmp, err := strconv.ParseInt(fields[2], 10, 32)
	if err != nil {
		klog.Errorf("valueParse failed: %v, str is %v", err, str)
		return
	}
	workerInfo.Port = int(tmp)

	if len(fields) > 3 {
		tmp, err = strconv.ParseInt(fields[3], 10, 32)
		if err != nil {
			klog.Errorf("valueParse failed: %v, str is %v", err, str)
			return
		}
		workerInfo.OptionPort = int(tmp)
	}
	return workerInfo
}

type MsgBusResolverBackend struct {
	client       *http.Client
	mu           sync.RWMutex
	instances    map[string]*structs.LLMInstance
	workerParser LLMWorkerParser

	resolvers map[string][]*MsgBusResolver
}

var (
	msgBusResolverBackend *MsgBusResolverBackend
	once                  sync.Once
)

func getOrCreateMsgBusResolverBackend(splitModel string) *MsgBusResolverBackend {
	once.Do(
		func() {
			var parser LLMWorkerParser
			if p, ok := parserMap[splitModel]; !ok {
				parser = parserMap[DefaultParserName]
			} else {
				parser = p
			}
			msgBusResolverBackend = &MsgBusResolverBackend{
				client:       utils.NewHttpClient(),
				resolvers:    make(map[string][]*MsgBusResolver),
				workerParser: parser,
			}
			go msgBusResolverBackend.syncWorkersLoop()
		},
	)
	return msgBusResolverBackend
}

func (r *MsgBusResolverBackend) SyncWorkerOnce() {
	newInstances, err := r.getInstances()
	if err != nil {
		return
	}

	oldInstances := r.instances

	// compare instance and log
	r.compareInstances(oldInstances, newInstances)

	// update each resolver and update current instances
	r.updateResolver(newInstances)
}

func (r *MsgBusResolverBackend) syncWorkersLoop() {
	for {
		r.SyncWorkerOnce()

		time.Sleep(time.Second * 5)
	}
}

func (r *MsgBusResolverBackend) getInstances() (map[string]*structs.LLMInstance, error) {
	kvs, err := r.readMessages()
	if err != nil {
		return nil, err
	}

	instances := make(map[string]*structs.LLMInstance)
	for _, kv := range kvs {
		podName := kv["__instance__"]
		instanceName := getInstanceName(podName)
		if _, ok := instances[instanceName]; !ok {
			instances[instanceName] = &structs.LLMInstance{
				Name:    instanceName,
				Workers: make([]*structs.LLMWorker, 0),
			}
		}
		for k, v := range kv {
			worker, err := r.workerParser.ParseWorker(k, v)
			if err != nil {
				klog.Errorf("parse worker failed: %v", err)
				continue
			}
			if worker == nil {
				continue
			}
			worker.PodName = podName
			instances[instanceName].Workers = append(instances[instanceName].Workers, worker)
		}
	}

	// check the instances worker consistency and fill common fields
	for _, instance := range instances {
		if len(instance.Workers) == 0 {
			continue
		}
		if ok, err := r.workerParser.CheckInstance(instance); !ok {
			klog.Errorf("instance %s is not consistent: %v", instance.Name, err)
		}
	}

	return instances, nil
}

// readMessages needs to distinguish whether it is a message-bus exception or the actual read data is empty
func (r *MsgBusResolverBackend) readMessages() (MsgKVs, error) {
	url := "http://127.0.0.1:9900/api/messages"
	req, _ := http.NewRequest("GET", url, nil)

	resp, err := r.client.Do(req)
	if err != nil {
		klog.Errorf("message-bus read failed: %v", err)
		return nil, fmt.Errorf("message-bus read failed: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		klog.Errorf("message-bus read data failed: %v", err)
		return nil, fmt.Errorf("message-bus read data failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		klog.Errorf("message-bus read failed, status code: %d", resp.StatusCode)
		return nil, fmt.Errorf("message-bus read failed, status code: %d", resp.StatusCode)
	}

	var kvs MsgKVs
	if err := json.Unmarshal(data, &kvs); err != nil {
		klog.Errorf("message-bus data unmarshal failed: %v, %s", err, string(data))
		return nil, fmt.Errorf("message-bus data unmarshal failed")
	}
	return kvs, nil
}

// trim the last index if the pod is fleet pod, only take the master node
func getInstanceName(podName string) string {
	strs := strings.Split(podName, "-")
	// if last index is number, it's a fleet pod, return the prefix
	if len(strs) > 0 && strs[len(strs)-1] != "" {
		if _, err := strconv.Atoi(strs[len(strs)-1]); err == nil {
			return strings.Join(strs[:len(strs)-1], "-")
		}
	}
	return podName
}

func (r *MsgBusResolverBackend) compareInstances(old, new map[string]*structs.LLMInstance) {
	oldWorkers := getWorkers(old)
	newWorkers := getWorkers(new)
	add := make(map[string]struct{})
	removed := make(map[string]struct{})
	for key := range newWorkers {
		if _, ok := oldWorkers[key]; !ok {
			add[key] = struct{}{}
		}
	}
	for key := range oldWorkers {
		if _, ok := newWorkers[key]; !ok {
			removed[key] = struct{}{}
		}
	}
	if len(add) > 0 {
		for key := range add {
			klog.Infof("add worker: %s on %v", key, newWorkers[key].Ep.String())
		}
	}
	if len(removed) > 0 {
		for key := range removed {
			klog.Infof("remove worker: %s on %v", key, oldWorkers[key].Ep.String())
		}
	}
}

func getWorkers(instances map[string]*structs.LLMInstance) map[string]*structs.LLMWorker {
	workers := make(map[string]*structs.LLMWorker)
	for _, instance := range instances {
		for _, worker := range instance.Workers {
			// unique key of each worker
			key := fmt.Sprintf("%s-%s-%s", worker.PodName, worker.WorkerId, worker.InferMode)
			workers[key] = worker
		}
	}
	return workers
}

func (r *MsgBusResolverBackend) registerResolver(inferMode string, resolver *MsgBusResolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolvers[inferMode] = append(r.resolvers[inferMode], resolver)
}

func (r *MsgBusResolverBackend) updateResolver(newInstances map[string]*structs.LLMInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, resolvers := range r.resolvers {
		var instances []*structs.LLMInstance
		// filter by infer mode
		for _, instance := range newInstances {
			if instance.InferMode == key {
				instances = append(instances, instance)
			}
		}
		// update each resolvers
		for _, resolver := range resolvers {
			resolver.updateInstances(instances)
		}
	}
	r.instances = newInstances
	return nil
}

type MsgBusResolver struct {
	mu sync.RWMutex

	inferMode    string
	instances    []*structs.LLMInstance
	splitMode    string
	workerParser LLMWorkerParser
}

func NewMsgBusResolver(inferMode string, splitMode string) Resolver {
	b := getOrCreateMsgBusResolverBackend(splitMode)
	r := &MsgBusResolver{
		inferMode:    inferMode,
		splitMode:    splitMode,
		workerParser: b.workerParser,
	}
	b.registerResolver(inferMode, r)
	// ensure the scheduler fetches the latest worker list after restart.
	b.SyncWorkerOnce()
	return r
}

func (r *MsgBusResolver) updateInstances(instances []*structs.LLMInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.instances = instances
}

func (r *MsgBusResolver) GetWeightEndpoints() (wEps []structs.WeightEndpoint) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, instance := range r.instances {
		for _, worker := range instance.Workers {
			// only return tp_rank == 0
			if worker.TPRank != 0 {
				continue
			}

			endpoint := structs.Endpoint{
				IP:         worker.Ep.IP,
				Port:       worker.Ep.Port,
				OptionPort: worker.Ep.OptionPort,
			}
			switch r.splitMode {
			case consts.SplitModeSGlangMooncake:
			default:
				// not need the dp info for now
				worker = nil
			}
			wEps = append(wEps, structs.WeightEndpoint{
				Weight: 100,
				Ep:     endpoint,
				Worker: worker,
			})
		}
	}

	return wEps
}
