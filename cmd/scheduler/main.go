package main

import (
	"flag"
	"llumnix/pkg/scheduler/service"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"

	"llumnix/cmd/config"
	"llumnix/cmd/scheduler/app/options"
)

func waitAndClean() {
	signalCh := make(chan os.Signal, 1)
	done := make(chan bool)

	signal.Notify(signalCh,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	go func() {
		cnt := 0
		for s := range signalCh {
			cnt += 1
			klog.Infof("received Signal[%v], stopping lb-gateway services ...", s.String())
			if cnt == 2 {
				done <- true
			}
		}
	}()

	<-done
}

func NewCommand() *cobra.Command {
	cfg := &options.SchedulerConfig{}
	cmd := &cobra.Command{
		Use: "scheduler",
		Run: func(cmd *cobra.Command, args []string) {
			if cfg.EnablePprof {
				klog.Infoln("enable pprof")
				go func() {
					klog.Infoln(http.ListenAndServe(":6061", nil))
				}()
			}
			config.ParseLlumnixExtraArgs(cmd.Flags(), cfg.ExtraArgs)
			klog.Infof("scheduler config: %+v", cfg)

			if cfg.StandaloneReschedulingMode {
				r := service.NewReschedulerService(cfg)
				klog.Info("llm rescheduler start ...")
				if err := r.Start(); err != nil {
					klog.Fatalf("llm rescheduler exit: %v", err)
				}
			} else {
				cs := service.NewSchedulerService(cfg)
				klog.Info("llm scheduler start ...")
				if err := cs.Start(); err != nil {
					klog.Fatalf("llm scheduler exit: %v", err)
				}
			}

			waitAndClean()
			klog.Info("service exited")
		},
	}
	cfg.AddFlags(cmd.Flags())
	return cmd
}

func main() {
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	defer klog.Flush()

	cmd := NewCommand()
	if err := cmd.Execute(); err != nil {
		klog.Fatalf("gateway cmd execute failed: %v", err)
	}
}
