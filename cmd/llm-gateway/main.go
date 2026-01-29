package main

import (
	"flag"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"

	"llumnix/cmd/llm-gateway/app/options"
	"llumnix/pkg/llm-gateway/service"
	"llumnix/pkg/llm-gateway/tokenizer"
)

func waitAndClean() {
	signal_ch := make(chan os.Signal, 1)
	done := make(chan bool)

	signal.Notify(signal_ch,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	go func() {
		cnt := 0
		for s := range signal_ch {
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
	cfg := &options.Config{}
	cmd := &cobra.Command{
		Use: "llm-gateway",
		Run: func(cmd *cobra.Command, args []string) {
			if cfg.EnablePprof {
				klog.Infoln("enable pprof")
				go func() {
					klog.Infoln(http.ListenAndServe(":6061", nil))
				}()
			}
			cfg.LoadCfgFromProperties()
			cfg.ParseLlumnixExtraArgs(cmd.Flags())
			klog.Infof("llm-gateway config: %+v", cfg)
			if cfg.ScheduleMode {
				cs := service.NewScheduleService(cfg)
				klog.Info("llm scheduler start ...")
				if err := cs.Start(); err != nil {
					klog.Fatalf("llm scheduler exit: %v", err)
				}
			} else if cfg.StandaloneRescheduleMode && cfg.SchedulerConfig.EnableRescheduling {
				r := service.NewRescheduleService(cfg)
				klog.Info("llm rescheduler start ...")
				if err := r.Start(); err != nil {
					klog.Fatalf("llm rescheduler exit: %v", err)
				}
			} else {
				// try init tokenizer
				tokenizer.InitTokenizer(cfg.TokenizerName, cfg.TokenizerPath, cfg.ChatTemplatePath)
				gw := service.NewGatewayService(cfg)
				klog.Info("llm gateway start ...")
				if err := gw.Start(); err != nil {
					klog.Fatalf("llm gateway exit: %v", err)
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
		klog.Fatalf("llm-gateway cmd execute failed: %v", err)
	}
}
