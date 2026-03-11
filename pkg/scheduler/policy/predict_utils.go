package policy

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/scheduler/predictor"
)

func predictNumComputedPrefillTokens(
	instances map[string]*instanceViewScheduling, ttftPredictor *predictor.QuadraticPredictor, maxNumBatchedTokens int32) {
	if !ttftPredictor.Fitted() {
		klog.V(4).Info("[predictNumComputedPrefillTokens] TTFT predictor not fitted, skip prediction")
		return
	}
	now := time.Now().UnixMilli()
	allPrefillsTokensNumMetric := allPrefillsTokensNum{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAllPrefillsTokensNum,
		},
	}
	for _, instance := range instances {
		// There may be an error of the order of 1ms to 10ms.
		elapsedTimeMs := float64(now - instance.cmsView.Status.TimestampMs)
		if elapsedTimeMs <= 0 {
			klog.Warningf("[predictNumComputedPrefillTokens] Instance %s elapsed time is negative, "+
				"skip prediction", instance.GetInstanceId())
			instance.schedulingCtx.numComputedPrefillTokensPredicted = 0
			continue
		}
		allPrefillsTokensNumMetric.Calculate(nil, instance)
		numUncomputedPrefillTokens := int32(allPrefillsTokensNumMetric.GetValue())
		numUncomputedRunningPrefillTokens :=
			instance.cmsView.Status.NumUncomputedTokensSchedulerRunningPrefills
		numComputedPrefillTokensPredicted := int32(0)
		for elapsedTimeMs > 0 && numUncomputedPrefillTokens > 0 {
			currNumBatchedTokens := int32(0)
			// NOTE(sunbiao.sun): This approximation is not completely correct when the status is pushed after update,
			// but it is a simple but very close to correct approximation, because it only affects the last chunk
			// when chunked prefill is enabled.
			if numUncomputedRunningPrefillTokens > 0 && numUncomputedRunningPrefillTokens < maxNumBatchedTokens {
				currNumBatchedTokens = numUncomputedRunningPrefillTokens
				numUncomputedRunningPrefillTokens = 0
			} else {
				currNumBatchedTokens = min(numUncomputedPrefillTokens, maxNumBatchedTokens)
			}
			currStepDurationMs, err := ttftPredictor.Predict(float64(currNumBatchedTokens))
			if err != nil {
				klog.Warningf("[predictNumComputedPrefillTokens] Predict error: %v", err)
				numComputedPrefillTokensPredicted = 0
				break
			}
			if ok, reason := validateStepDurationMs(currStepDurationMs); !ok {
				klog.Warningf(
					"[predictNumComputedPrefillTokens] invalid step duration %f ms for batch=%d, reason=%s",
					currStepDurationMs, currNumBatchedTokens, reason)
				numComputedPrefillTokensPredicted = 0
				break
			}
			if elapsedTimeMs >= currStepDurationMs {
				numComputedPrefillTokensPredicted += currNumBatchedTokens
				elapsedTimeMs -= currStepDurationMs
				numUncomputedPrefillTokens -= currNumBatchedTokens
			} else {
				// linearly apportion
				numComputedPrefillTokensPredicted +=
					int32(math.Round(float64(currNumBatchedTokens) * elapsedTimeMs / currStepDurationMs))
				elapsedTimeMs = 0
				numUncomputedPrefillTokens = 0
			}
		}
		instance.schedulingCtx.numComputedPrefillTokensPredicted = numComputedPrefillTokensPredicted
		if numComputedPrefillTokensPredicted > numUncomputedPrefillTokens {
			klog.Warningf(
				"[predictNumComputedPrefillTokens] predicted computed prefill tokens %v is larger than "+
					"uncomputed prefill tokens %v, truncate to uncomputed prefill tokens",
				numComputedPrefillTokensPredicted, numUncomputedPrefillTokens)
			instance.schedulingCtx.numComputedPrefillTokensPredicted = numUncomputedPrefillTokens
		}
	}
}

func validateStepDurationMs(stepMs float64) (bool, string) {
	if math.IsNaN(stepMs) {
		return false, "NaN"
	}
	if math.IsInf(stepMs, 0) {
		return false, "Inf"
	}
	if stepMs <= 0 {
		return false, "non-positive"
	}

	const (
		minStepMs = 1e-3
		maxStepMs = 1e3 * 1e3
	)
	if stepMs < minStepMs {
		return false, fmt.Sprintf("too small (%f < %f)", stepMs, minStepMs)
	}
	if stepMs > maxStepMs {
		return false, fmt.Sprintf("too large (%f > %f)", stepMs, maxStepMs)
	}
	return true, ""
}

var (
	latencyPredictor *LatencyPredictor
	once             sync.Once
)

type TtftProfilingResult struct {
	TokensNum int       `json:"tokens_num"`
	Mean      float64   `json:"mean"`
	Std       float64   `json:"std"`
	Min       float64   `json:"min"`
	Max       float64   `json:"max"`
	P50       float64   `json:"p50"`
	P95       float64   `json:"p95"`
	P99       float64   `json:"p99"`
	RawData   []float64 `json:"raw_data"`
}

type TtftData struct {
	Metadata struct {
		Model       string `json:"model"`
		Timestamp   string `json:"timestamp"`
		Description string `json:"description"`
	} `json:"metadata"`
	Results []TtftProfilingResult `json:"results"`
}

type ItlResult struct {
	BatchSize        int       `json:"batch_size"`
	TokensPerRequest int       `json:"tokens_per_request"`
	Mean             float64   `json:"mean"`
	Std              float64   `json:"std"`
	Min              float64   `json:"min"`
	Max              float64   `json:"max"`
	P50              float64   `json:"p50"`
	P95              float64   `json:"p95"`
	P99              float64   `json:"p99"`
	RawData          []float64 `json:"raw_data"`
}

type ITLData struct {
	Metadata struct {
		Model       string `json:"model"`
		Timestamp   string `json:"timestamp"`
		Description string `json:"description"`
	} `json:"metadata"`
	Results []ItlResult `json:"results"`
}

func loadTtftData(filepath string) (*TtftData, error) {
	file, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var data TtftData
	if err := json.Unmarshal(file, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if len(data.Results) == 0 {
		return nil, fmt.Errorf("no profiling results found in file")
	}

	return &data, nil
}

func loadTpotData(filepath string) (*ITLData, error) {
	file, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var data ITLData
	if err := json.Unmarshal(file, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if len(data.Results) == 0 {
		return nil, fmt.Errorf("no profiling results found in file")
	}

	return &data, nil
}

func GetLatencyPredictor(ttftProfilingPath string, tpotProfilingPath string) *LatencyPredictor {
	once.Do(func() {
		var err error

		ttftData, err := loadTtftData(ttftProfilingPath)
		if err != nil {
			klog.Fatalf("[GetLatencyPredictor] Failed to load TTFT data from %s: %v",
				ttftProfilingPath, err)
		}

		tpotData, err := loadTpotData(tpotProfilingPath)
		if err != nil {
			klog.Fatalf("[GetLatencyPredictor] Failed to load TPOT data from %s: %v",
				tpotProfilingPath, err)
		}

		ttftPredictor := predictor.NewInterpolationPredictor()
		for _, result := range ttftData.Results {
			ttftPredictor.AddSample(float64(result.TokensNum), 0, result.P50)
		}

		tpotPredictor := predictor.NewInterpolationPredictor()
		for _, result := range tpotData.Results {
			tpotPredictor.AddSample(float64(result.BatchSize), float64(result.TokensPerRequest), result.P50)
		}

		latencyPredictor = &LatencyPredictor{
			ttftPredictor: ttftPredictor,
			tpotPredictor: tpotPredictor,
		}

		klog.Infof("Initialized LatencyPredictor with %d TTFT points and %d TPOT points",
			len(ttftData.Results), len(tpotData.Results))
	})

	return latencyPredictor
}

// LatencyPredictor If error occurs, return math.Inf(1)
type LatencyPredictor struct {
	ttftPredictor *predictor.InterpolationPredictor
	tpotPredictor *predictor.InterpolationPredictor
}

func (lp *LatencyPredictor) predictTtftLatencyByChunkPrefill(
	prefillTokensNum int32,
	decodeReqsNum int32,
	decodeTokensNum int32,
	maxNumBatchedTokens float64) (float64, error) {

	remainingPrefillTokens := float64(prefillTokensNum)
	totalLatency := 0.0

	decodeStepLatency, err := lp.predictTpotLatency(decodeReqsNum, decodeTokensNum)
	if err != nil {
		klog.Warningf("[predictTtftLatencyByChunkPrefill] Predict error: %v", err)
		return math.Inf(1), err
	}

	totalSteps := 0

	for remainingPrefillTokens > 0 {
		curStepPrefillTokensNum := min(remainingPrefillTokens, maxNumBatchedTokens)
		remainingPrefillTokens -= curStepPrefillTokensNum
		totalSteps += 1

		stepLatency, err := lp.ttftPredictor.Predict(curStepPrefillTokensNum, 0)

		if err != nil {
			klog.Warningf("[predictTtftLatencyByChunkPrefill] Predict error: %v", err)
			return math.Inf(1), err
		}

		totalLatency = totalLatency + stepLatency + decodeStepLatency
	}

	klog.V(3).Infof("Predict ttft latency by chunk prefill, value: %f,"+
		" decodeStepLatency: %f, totalSteps: %d, [prefillTokensNum: %d, decodeReqsNum:"+
		" %d, decodeTokensNum: %d, maxNumBatchedTokens: %f]", totalLatency, decodeStepLatency,
		totalSteps, prefillTokensNum, decodeReqsNum, decodeTokensNum, maxNumBatchedTokens)

	return totalLatency, nil
}

func (lp *LatencyPredictor) predictTpotLatency(
	decodeReqsNum int32, decodeTokensNum int32) (float64, error) {
	if decodeReqsNum <= 0 || decodeTokensNum <= 0 {
		return 0, nil
	}

	var TokensPerReq float64
	TokensPerReq = float64(decodeTokensNum) / float64(decodeReqsNum)

	if TokensPerReq < 8.0 {
		TokensPerReq = 8.0
	}

	predictLatency, err := lp.tpotPredictor.Predict(float64(decodeReqsNum), TokensPerReq)
	if err != nil {
		klog.Warningf("[predictTpotLatency] Predict error: %v", err)
		return math.Inf(1), err
	}

	klog.V(3).Infof("Predict tpot latency, value: %f, [decodeReqsNum: %d, decodeTokensNum: %d, TokensPerReq: %f]",
		predictLatency, decodeReqsNum, decodeTokensNum, TokensPerReq)

	return predictLatency, nil
}
