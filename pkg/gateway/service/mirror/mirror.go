package mirror

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/types"
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
	mirrorConfig := propertyManager.GetMirrorConfig()
	return mirrorConfig.Enable
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
		mirrorConfig := propertyManager.GetMirrorConfig()

		if mirrorConfig.EnableLog {
			klog.Infof("[%s] Mirror request enabled, target: %s, ratio: %.2f, token: %s, timeout: %.2f",
				req.Id, mirrorConfig.Target, mirrorConfig.Ratio, mirrorConfig.Authorization, mirrorConfig.Timeout)
		}

		// If mirror target is not configured or ratio is not positive, skip mirroring
		if mirrorConfig.Target == "" || mirrorConfig.Ratio <= 0 {
			return
		}

		// Check if we should mirror this request based on the ratio
		if float64(rand.Intn(100)) >= mirrorConfig.Ratio {
			return
		}

		httpReq := req.HttpRequest.Request
		data := req.LLMRequest.RawData

		mirrorURL := fmt.Sprintf("%s%s", mirrorConfig.Target, httpReq.URL.Path)
		mirrorReq, err := http.NewRequest(
			httpReq.Method,
			mirrorURL,
			bytes.NewBufferString(data),
		)
		if err != nil {
			if mirrorConfig.EnableLog {
				klog.Errorf("Failed to create mirror request: %v", err)
			}
			return
		}

		mirrorReq.Header = httpReq.Header.Clone()
		mirrorReq.Header.Del("Content-Length")

		if mirrorConfig.Authorization != "" {
			mirrorReq.Header.Set("Authorization", mirrorConfig.Authorization)
		}

		if mirrorConfig.Timeout == 0 {
			mirrorReq = mirrorReq.WithContext(req.Context)
		} else {
			ctx, cancel := context.WithTimeout(req.Context, time.Duration(mirrorConfig.Timeout)*time.Millisecond)
			defer cancel()
			mirrorReq = mirrorReq.WithContext(ctx)
		}
		resp, err := m.client.Do(mirrorReq)
		if err != nil && mirrorConfig.EnableLog {
			klog.Errorf("Mirror request failed: %v", err)
			return
		}
		defer resp.Body.Close()

		io.ReadAll(resp.Body)

		if mirrorConfig.EnableLog {
			klog.Infof("[%s] Mirror request sent to %s, status: %d", req.Id, mirrorURL, resp.StatusCode)
		}
	}()
}
