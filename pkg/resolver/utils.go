package resolver

import (
	"fmt"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
	"strings"

	"k8s.io/klog/v2"
)

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
		return nil
	}
	return schResolver
}

// CreateBackendServiceResolver creates an LLM resolver based on the provided configuration and role.
// It supports message bus discovery and EAS service discovery.
func CreateBackendServiceResolver(config *options.Config, role types.InferRole) LLMResolver {
	buildArgs := BuildArgs{"role": role.String()}

	// Handle local test mode first
	if len(config.LocalTestIPs) > 0 {
		uri := fmt.Sprintf("llm+endpoints://%s", config.LocalTestIPs)
		r, err := BuildLlmResolver(uri, buildArgs)
		if err != nil {
			klog.Fatalf("create endpoints resolver failed: %v", err)
		}
		return r
	}

	// Support Gateway+Scheduler integration test mode (uses CompositeBalancer with SchedulerClient)
	if len(config.LocalTestBackendIPs) > 0 {
		uri := fmt.Sprintf("llm+endpoints://%s", config.LocalTestBackendIPs)
		r, err := BuildLlmResolver(uri, buildArgs)
		if err != nil {
			klog.Fatalf("create endpoints resolver from LocalTestBackendIPs failed: %v", err)
		}
		return r
	}

	switch config.UseDiscovery {
	case consts.DiscoveryMessageBus:
		r, err := BuildLlmResolver(MsgBusURI, buildArgs)
		if err != nil {
			klog.Fatalf("create msgbus resolver failed: %v", err)
		}
		return r
	case consts.DiscoveryRedis:
		uri := RedisUriPrefix + fmt.Sprintf("%s:%s",
			config.LlumnixConfig.CmsRedisHost, config.LlumnixConfig.CmsRedisPort)
		buildArgs["redis_username"] = config.LlumnixConfig.CmsRedisUsername
		buildArgs["redis_password"] = config.LlumnixConfig.CmsRedisPassword
		buildArgs["redis_socketTimeout"] = config.LlumnixConfig.CmsRedisSocketTimeout
		buildArgs["redis_retryTimes"] = config.LlumnixConfig.CmsRedisRetryTimes
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
