package main

import (
	"flag"
	"llumnix/cmd/config"
	"llumnix/cmd/gateway/app/options"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"

	"llumnix/pkg/llm-gateway/service"
	"llumnix/pkg/llm-gateway/tokenizer"
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
	cfg := &options.GatewayConfig{}
	cmd := &cobra.Command{
		Use: "gateway",
		Run: func(cmd *cobra.Command, args []string) {
			if cfg.EnablePprof {
				klog.Infoln("enable pprof")
				go func() {
					klog.Infoln(http.ListenAndServe(":6061", nil))
				}()
			}

			cfg.LoadCfgFromProperties()
			config.ParseLlumnixExtraArgs(cmd.Flags(), cfg.ExtraArgs)
			klog.Infof("llm-gateway config: %+v", cfg)

			tokenizer.InitTokenizer(cfg.TokenizerName, cfg.TokenizerPath, cfg.ChatTemplatePath)

			gw := service.NewGatewayService(cfg)
			klog.Info("llm gateway start ...")

			if err := gw.Start(); err != nil {
				klog.Fatalf("llm gateway exit: %v", err)
			}

			waitAndClean()
			klog.Info("gateway service exited")
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
