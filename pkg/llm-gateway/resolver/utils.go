package resolver

import (
	"fmt"
	"llumnix/cmd/llm-gateway/app/options"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/types"
	"strings"

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
func CreateSchedulerResolver(config *options.Config) Resolver {
	var (
		schResolver Resolver
		err         error
	)
	if config.LocalTestSchedulerIP != "" {
		uri := fmt.Sprintf("endpoints://%s", config.LocalTestSchedulerIP)
		schResolver, err = BuildResolver(uri, BuildArgs{})
	} else {
		parts := strings.Split(config.LlmScheduler, ".")
		uri := fmt.Sprintf("eas://%s?include=%s", parts[0], config.LlmScheduler)
		schResolver, err = BuildResolver(uri, BuildArgs{})
	}
	if err != nil {
		klog.Fatalf("create scheduler resolver failed: %v", err)
	}
	return schResolver
}

// CreateBackendServiceResolver creates an LLM resolver based on the provided configuration and role.
// It supports message bus discovery and EAS service discovery.
func CreateBackendServiceResolver(config *options.Config, role types.InferRole) LLMResolver {
	buildArgs := BuildArgs{"role": role.String()}

	switch config.UseDiscovery {
	case consts.DiscoveryRedis:
		uri := RedisUriPrefix + fmt.Sprintf("%s:%s",
			config.SchedulerConfig.CmsRedisHost, config.SchedulerConfig.CmsRedisPort)
		buildArgs["redis_username"] = config.SchedulerConfig.CmsRedisUsername
		buildArgs["redis_password"] = config.SchedulerConfig.CmsRedisPassword
		buildArgs["redis_socketTimeout"] = config.SchedulerConfig.CmsRedisSocketTimeout
		buildArgs["redis_retryTimes"] = config.SchedulerConfig.CmsRedisRetryTimes
		buildArgs["redis_discovery_refresh_interval_ms"] = config.RedisDiscoveryRefreshIntervalMs
		buildArgs["redis_discovery_status_ttl"] = config.RedisDiscoveryStatusTTLMs
		r, err := BuildLlmResolver(uri, buildArgs)
		if err != nil {
			klog.Fatalf("create redis resolver failed: %v", err)
		}
		return r
	default:
		parts := strings.Split(config.LlmScheduler, ".")
		if len(parts) < 2 {
			klog.Fatalf("invalid scheduler service name: %s", config.LlmScheduler)
		}
		group := parts[0]

		excludeService := fmt.Sprintf("%s,%s", config.LlmScheduler, config.LlmGateway)
		if len(config.Redis) > 0 {
			excludeService = fmt.Sprintf("%s,%s", excludeService, config.Redis)
		}
		uri := fmt.Sprintf("llm+eas://%s?exclude=%s", group, excludeService)
		r, err := BuildLlmResolver(uri, buildArgs)
		if err != nil {
			klog.Fatalf("create llm eas resolver failed: %v", err)
		}
		return r
	}
}
