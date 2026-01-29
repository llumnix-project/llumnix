package llumnix

import (
	"fmt"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/predictor"
	"math"
	"time"

	"k8s.io/klog/v2"
)

func predictNumComputedPrefillBlocks(
	instances map[string]*instanceViewScheduling, ttftPredictor *predictor.QuadraticPredictor, kvCacheBlockSize int32,
	maxNumBatchedTokens int32) {
	if ttftPredictor.Fitted() == false {
		klog.V(4).Info("[predictNumComputedPrefillBlocks] TTFT predictor not fitted, skip prediction")
		return
	}
	now := time.Now().UnixMilli()
	allPrefillsKVBlocksNumMetric := AllPrefillsKVBlocksNum{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAllPrefillsKVBlocksNum,
		},
	}
	for _, instance := range instances {
		// There may be an error of the order of 1ms to 10ms.
		elapsedTimeMs := float64(now - instance.cmsView.Status.TimestampMs)
		if elapsedTimeMs <= 0 {
			klog.Warningf("[predictNumComputedPrefillBlocks] Instance %s elapsed time is negative, "+
				"skip prediction", instance.GetInstanceId())
			instance.schedulingCtx.numComputedPrefillBlocksPredicted = 0
			continue
		}
		allPrefillsKVBlocksNumMetric.Calculate(instance)
		numUncomputedPrefillBlocks := int32(allPrefillsKVBlocksNumMetric.GetValue())
		numUncomputedPrefillTokens := numUncomputedPrefillBlocks * kvCacheBlockSize
		numUncomputedRunningPrefillTokens :=
			instance.cmsView.Status.NumUncomputedBlocksSchedulerRunningPrefills * kvCacheBlockSize
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
			currStepDurationMs, err :=
				ttftPredictor.Predict(
					float64((currNumBatchedTokens + kvCacheBlockSize - 1) /
						kvCacheBlockSize))
			if err != nil {
				klog.Warningf("[predictNumComputedPrefillBlocks] Predict error: %v", err)
				numComputedPrefillTokensPredicted = 0
				break
			}
			if ok, reason := validateStepDurationMs(currStepDurationMs); !ok {
				klog.Warningf(
					"[predictNumComputedPrefillBlocks] invalid step duration %f ms for batch=%d, reason=%s",
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
		currNumComputedPrefillBlocksPredicted :=
			(numComputedPrefillTokensPredicted + kvCacheBlockSize - 1) / kvCacheBlockSize
		instance.schedulingCtx.numComputedPrefillBlocksPredicted = currNumComputedPrefillBlocksPredicted
		if currNumComputedPrefillBlocksPredicted > numUncomputedPrefillBlocks {
			klog.Warningf(
				"[predictNumComputedPrefillBlocks] predicted computed prefill blocks %v is larger than "+
					"uncomputed prefill blocks %v, truncate to uncomputed prefill blocks",
				currNumComputedPrefillBlocksPredicted, numUncomputedPrefillBlocks)
			instance.schedulingCtx.numComputedPrefillBlocksPredicted = numUncomputedPrefillBlocks
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
