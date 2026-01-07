package consts

import "k8s.io/klog/v2"

var instanceType2InferModeMap = map[string]string{
	LlumnixNeutralInstanceType: NormalInferMode,
	LlumnixPrefillInstanceType: PrefillInferMode,
	LlumnixDecodeInstanceType:  DecodeInferMode,
}

func TransformInstanceType2InferMode(instanceType string) string {
	if mode, exists := instanceType2InferModeMap[instanceType]; exists {
		return mode
	}
	klog.Warningf("Unknown instance type: %s", instanceType)
	return ""
}
