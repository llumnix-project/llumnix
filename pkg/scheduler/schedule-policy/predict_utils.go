package schedule_policy

import (
	"fmt"
	"math"
	"time"

	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/scheduler/predictor"
)

func predictNumComputedPrefillTokens(
	instances map[string]*instanceViewScheduling, ttftPredictor *predictor.QuadraticPredictor, maxNumBatchedTokens int32) {
	if ttftPredictor.Fitted() == false {
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
		allPrefillsTokensNumMetric.Calculate(instance)
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
