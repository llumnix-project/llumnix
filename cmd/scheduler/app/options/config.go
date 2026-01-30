package options

import (
	"flag"
	"llumnix/cmd/config"

	"github.com/spf13/pflag"
)

type SchedulerConfig struct {
	Port int
	Host string

	MultiModelSupport bool

	// ColocatedRescheduleMode means whether to start a rescheduler inside a scheduler process
	// (so that they can share the cms client).
	ColocatedRescheduleMode bool
	// StandaloneRescheduleMode means whether to start a rescheduler as a separate process.
	StandaloneRescheduleMode bool

	// enable input log
	EnableLogInput bool
	EnablePprof    bool

	ExtraArgs string

	config.DiscoveryConfig
	config.PDSplitConfig
	config.ScheduleBaseConfig
	config.LiteModeScheduleConfig
	config.FullModeScheduleConfig
}

func (c *SchedulerConfig) AddFlags(flags *pflag.FlagSet) {
	c.AddConfigFlags(flags)
	c.DiscoveryConfig.AddDiscoveryConfigFlags(flags)
	c.PDSplitConfig.AddPDSplitConfigFlags(flags)
	c.ScheduleBaseConfig.AddScheduleBaseConfigFlags(flags)
	c.LiteModeScheduleConfig.AddLiteModeScheduleConfigFlags(flags)
	c.FullModeScheduleConfig.AddFullModeScheduleConfigFlags(flags)
	flags.AddGoFlagSet(flag.CommandLine)
}

func (c *SchedulerConfig) AddConfigFlags(flags *pflag.FlagSet) {
	flags.IntVar(&c.Port, "port", 8002, "http service listen port")
	flags.StringVar(&c.Host, "host", "0.0.0.0", "http service listen host")

	flags.BoolVar(&c.MultiModelSupport, "multi-model-support", false, "enable multi model support")

	flags.BoolVar(&c.ColocatedRescheduleMode, "colocated-reschedule-mode", false, "enable colocated reschedule mode")
	flags.BoolVar(&c.StandaloneRescheduleMode, "standalone-reschedule-mode", false, "enable standalone reschedule mode")

	flags.BoolVar(&c.EnableLogInput, "enable-log-input", false, "enable log input or not")
	flags.BoolVar(&c.EnablePprof, "enable-pprof", false, "enable pprof")
	flags.StringVar(&c.ExtraArgs, "extra-args", "", "Llumnix extra args")
}

func (c *SchedulerConfig) EnableRequestStateTracking() bool {
	return !c.EnableFullModeScheduling && !c.StandaloneRescheduleMode
}
