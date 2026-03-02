package resolver

import (
	"fmt"
	"llumnix/cmd/config"
	"llumnix/pkg/consts"

	"k8s.io/klog/v2"
)

// DiffSets calculates the difference between two slices of comparable items.
// It returns two slices: added items (in new but not in old) and removed items (in old but not in new).
// The keyFunc is used to generate a unique key for each item for comparison.
func DiffSets[T any](old, new []T, keyFunc func(T) string) (added, removed []T) {
	oldMap := make(map[string]T, len(old))
	for _, item := range old {
		oldMap[keyFunc(item)] = item
	}

	// Find added items and mark existing items
	added = make([]T, 0, len(new))
	for _, item := range new {
		key := keyFunc(item)
		if _, exists := oldMap[key]; exists {
			delete(oldMap, key)
		} else {
			added = append(added, item)
		}
	}

	// Remaining items in oldMap are removed items
	removed = make([]T, 0, len(oldMap))
	for _, item := range oldMap {
		removed = append(removed, item)
	}

	return added, removed
}

// CreateSchedulerResolver creates a scheduler resolver based on the provided configuration.
func CreateSchedulerResolver(config *config.DiscoveryConfig) Resolver {
	switch config.SchedulerDiscovery {
	case consts.DiscoveryEndpoints:
		uri := fmt.Sprintf("endpoints://%s", config.SchedulerEndpoints)
		schResolver, err := BuildResolver(uri, BuildArgs{})
		if err != nil {
			klog.Fatalf("create scheduler resolver failed: %v", err)
		}
		return schResolver
	default:
		panic("unsupported discovery type")
	}
}

// CreateBackendServiceResolver creates an LLM resolver based on the provided configuration and infer type.
// It supports message bus discovery and EAS service discovery.
func CreateBackendServiceResolver(config *config.DiscoveryConfig, inferType consts.InferType) LLMResolver {
	buildArgs := BuildArgs{"instance_type": inferType.String()}

	switch config.LLMBackendDiscovery {
	case consts.DiscoveryRedis:
		uri := RedisUriPrefix + fmt.Sprintf("%s:%d",
			config.DiscoveryRedisHost, config.DiscoveryRedisPort)
		buildArgs["redis_username"] = config.DiscoveryRedisUsername
		buildArgs["redis_password"] = config.DiscoveryRedisPassword
		buildArgs["redis_socketTimeout"] = config.DiscoveryRedisSocketTimeout
		buildArgs["redis_retryTimes"] = config.DiscoveryRedisRetryTimes
		buildArgs["redis_discovery_refresh_interval_ms"] = config.DiscoveryRedisRefreshIntervalMs
		buildArgs["redis_discovery_status_ttl"] = config.DiscoveryRedisStatusTTLMs
		r, err := BuildLlmResolver(uri, buildArgs)
		if err != nil {
			klog.Fatalf("create redis resolver failed: %v", err)
		}
		return r
	case consts.DiscoveryEndpoints:
		uri := fmt.Sprintf("%s://%s", EndpointsLlmUriPrefix, config.LLMBackendEndpoints)
		r, err := BuildLlmResolver(uri, buildArgs)
		if err != nil {
			klog.Fatalf("create endpoints resolver failed: %v", err)
		}
		return r
	default:
		panic("unsupported discovery type")
	}
}
