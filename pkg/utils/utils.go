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

// DiffSets calculates the difference between two slices of comparable items.
// It returns two slices: added items (in new but not in old) and removed items (in old but not in new).
// The keyFunc is used to generate a unique key for each item for comparison.
func DiffSets[T any](old, new []T, keyFunc func(T) string) (added, removed []T) {
	oldMap := make(map[string]T, len(old))
	for _, item := range old {
		oldMap[keyFunc(item)] = item
	}

	added = make([]T, 0, len(new))
	for _, item := range new {
		key := keyFunc(item)
		if _, exists := oldMap[key]; exists {
			delete(oldMap, key)
		} else {
			added = append(added, item)
		}
	}

	removed = make([]T, 0, len(oldMap))
	for _, item := range oldMap {
		removed = append(removed, item)
	}

	return added, removed
}
