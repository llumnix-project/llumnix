package options

import (
	"flag"
	"time"

	"github.com/spf13/pflag"

	"llumnix/cmd/config"
	"llumnix/pkg/consts"
	"llumnix/pkg/gateway/property"
)

type GatewayConfig struct {
	Port int
	Host string

	// max queue size
	MaxRequestBufferQueueSize int
	// number of coroutines which read the requests from queue
	WaitQueueThreads int

	ServiceToken string
	// waiting schedule timeout if no schedule result, unit is milliseconds, 0 means that drop request
	WaitScheduleTimeout time.Duration
	// retry interval of waiting schedule results, unit is milliseconds
	WaitScheduleRetryInterval time.Duration
	// whether forward tokens to scheduler
	ForwardTokens bool

	configManager *property.ConfigManager

	EnableLogInput bool
	EnablePprof    bool

	// overwrite parameters
	ExtraArgs string

	config.DiscoveryConfig
	config.ProcessorConfig
	config.RouteConfig
	config.PDDisaggConfig
	config.ScheduleBaseConfig
	config.LiteModeScheduleConfig
	config.BatchServiceConfig
}

func (c *GatewayConfig) AddFlags(flags *pflag.FlagSet) {
	c.AddConfigFlags(flags)
	c.AddDiscoveryConfigFlags(flags)
	c.AddScheduleBaseConfigFlags(flags)
	c.AddLiteModeScheduleConfigFlags(flags)
	c.AddPDDisaggConfigFlags(flags)
	c.AddRouteConfigFlags(flags)
	c.AddProcessorConfigFlags(flags)
	c.AddBatchServiceConfigFlags(flags)
	flags.AddGoFlagSet(flag.CommandLine)
}

func (c *GatewayConfig) AddConfigFlags(flags *pflag.FlagSet) {
	flags.IntVar(&c.Port, "port", 8001, "http service listen port")
	flags.StringVar(&c.Host, "host", "0.0.0.0", "http service listen host")

	flags.IntVar(&c.MaxRequestBufferQueueSize, "max-queue-size", 512, "max buffer queue size")
	flags.IntVar(&c.WaitQueueThreads, "wait-queue-threads", 5, "number of coroutines which read the requests from queue")

	flags.StringVar(&c.ServiceToken, "service-token", "", "service token")
	flags.DurationVar(&c.WaitScheduleTimeout, "wait-schedule-timeout", 10000*time.Millisecond, "waiting timeout if no free token")
	flags.DurationVar(&c.WaitScheduleRetryInterval, "wait-schedule-try-period", 1000*time.Millisecond, "retry period while waiting free tokens")
	flags.BoolVar(&c.ForwardTokens, "forward-tokens", true, "whether forward tokens to scheduler")

	flags.BoolVar(&c.EnableLogInput, "enable-log-input", false, "enable log input or not")
	flags.BoolVar(&c.EnablePprof, "enable-pprof", false, "enable pprof")
	flags.StringVar(&c.ExtraArgs, "extra-args", "", "Llumnix extra args")
}

func (c *GatewayConfig) GetConfigManager() *property.ConfigManager {
	return c.configManager
}

func (c *GatewayConfig) IsPDDisagg() bool {
	return c.PDDisaggProtocol != ""
}

func (c *GatewayConfig) IsPDRoundRobin() bool {
	return c.IsPDDisagg() && c.SchedulePolicy == consts.SchedulePolicyRoundRobin
}

func (c *GatewayConfig) EnableRequestStateTracking() bool {
	return c.RequestStateReportInterval > 0 && !c.EnableFullModeScheduling
}

func (c *GatewayConfig) LoadCfgFromProperties() {
	const propertyFile = "/mnt/mirror.json"
	configPaths := map[string]string{
		"mirror": propertyFile,
	}
	c.configManager = property.NewConfigManager(configPaths)
}
