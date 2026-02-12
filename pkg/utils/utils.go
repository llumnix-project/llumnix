package utils

import (
	"k8s.io/klog/v2"

	"llm-gateway/pkg/consts"
)

var instanceType2InferModeMap = map[string]string{
	consts.LlumnixNeutralInstanceType: consts.NormalInferMode,
	consts.LlumnixPrefillInstanceType: consts.PrefillInferMode,
	consts.LlumnixDecodeInstanceType:  consts.DecodeInferMode,
}

func TransformInstanceType2InferMode(instanceType string) string {
	if mode, exists := instanceType2InferModeMap[instanceType]; exists {
		return mode
	}
	klog.Warningf("Unknown instance type: %s", instanceType)
	return ""
}
