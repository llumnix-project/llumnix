package utils

import (
	"llumnix/pkg/consts"

	"k8s.io/klog/v2"
)

var instanceType2InferModeMap = map[string]string{
	consts.NeutralInstanceType: consts.NormalInferMode,
	consts.PrefillInstanceType: consts.PrefillInferMode,
	consts.DecodeInstanceType:  consts.DecodeInferMode,
}

func TransformInstanceType2InferMode(instanceType string) string {
	if mode, exists := instanceType2InferModeMap[instanceType]; exists {
		return mode
	}
	klog.Warningf("Unknown instance type: %s", instanceType)
	return ""
}
