package mirror

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/llm-gateway/types"
	"math/rand"
	"net"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

type Mirror struct {
	config *options.GatewayConfig
	client *http.Client
}

func NewMirror(config *options.GatewayConfig) *Mirror {
	return &Mirror{
		config: config,
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			},
		},
	}
}

func (m *Mirror) Enabled() bool {
	propertyManager := m.config.GetConfigManager()
	if propertyManager == nil {
		return false
	}
	return propertyManager.GetBoolWithDefault("llm_gateway.traffic_mirror.enable", false)
}

// TryMirror sends a copy of the request to the mirror target based on the configured ratio
func (m *Mirror) TryMirror(req *types.RequestContext) {
	// Perform the mirror request in a separate goroutine to avoid blocking
	go func() {
		defer func() {
			if r := recover(); r != nil {
				klog.Errorf("Panic in mirror request cloning: %v", r)
			}
		}()

		// Get the newest mirror configuration directly from property manager
		propertyManager := m.config.GetConfigManager()
		mirrorTarget := propertyManager.GetStringWithDefault("llm_gateway.traffic_mirror.target", "")
		mirrorRatio := propertyManager.GetFloatWithDefault("llm_gateway.traffic_mirror.ratio", 0.0)
		mirrorToken := propertyManager.GetStringWithDefault("llm_gateway.traffic_mirror.token", "")
		mirrorTimeout := propertyManager.GetFloatWithDefault("llm_gateway.traffic_mirror.timeout", 0.0)
		mirrorLog := propertyManager.GetBoolWithDefault("llm_gateway.traffic_mirror.enable_log", false)
		if mirrorLog {
			klog.Infof("[%s] Mirror request enabled, target: %s, ratio: %.2f, token: %s, timeout: %.2f", req.Id, mirrorTarget, mirrorRatio, mirrorToken, mirrorTimeout)
		}

		// If mirror target is not configured or ratio is not positive, skip mirroring
		if mirrorTarget == "" || mirrorRatio <= 0 {
			return
		}

		// Check if we should mirror this request based on the ratio
		if float64(rand.Intn(100)) >= mirrorRatio {
			return
		}

		httpReq := req.HttpRequest.Request
		data := req.LLMRequest.RawData

		mirrorURL := fmt.Sprintf("%s%s", mirrorTarget, httpReq.URL.Path)
		mirrorReq, err := http.NewRequest(
			httpReq.Method,
			mirrorURL,
			bytes.NewBufferString(data),
		)
		if err != nil {
			if mirrorLog {
				klog.Errorf("Failed to create mirror request: %v", err)
			}
			return
		}

		mirrorReq.Header = httpReq.Header.Clone()
		mirrorReq.Header.Del("Content-Length")

		if mirrorToken != "" {
			mirrorReq.Header.Set("Authorization", mirrorToken)
		}

		if mirrorTimeout == 0 {
			mirrorReq = mirrorReq.WithContext(req.Context)
		} else {
			ctx, cancel := context.WithTimeout(req.Context, time.Duration(mirrorTimeout)*time.Millisecond)
			defer cancel()
			mirrorReq = mirrorReq.WithContext(ctx)
		}
		resp, err := m.client.Do(mirrorReq)
		if err != nil {
			if mirrorLog {
				klog.Errorf("Mirror request failed: %v", err)
			}
			return
		}
		defer resp.Body.Close()

		io.ReadAll(resp.Body)

		if mirrorLog {
			klog.Infof("[%s] Mirror request sent to %s, status: %d", req.Id, mirrorURL, resp.StatusCode)
		}
	}()
}
