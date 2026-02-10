package mirror

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/property"
	"llm-gateway/pkg/types"
	"math/rand"
	"net"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

type Mirror struct {
	config *options.Config
	client *http.Client
}

func NewMirror(config *options.Config) *Mirror {
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
	propertyManager := property.GetDynamicConfigManager()
	return propertyManager.GetBoolWithDefault(consts.ConfigKeyMirrorEnable, false)
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
		propertyManager := property.GetDynamicConfigManager()
		mirrorTarget := propertyManager.GetStringWithDefault(consts.ConfigKeyMirrorTarget, "")
		mirrorRatio := propertyManager.GetFloatWithDefault(consts.ConfigKeyMirrorRatio, 0.0)
		mirrorToken := propertyManager.GetStringWithDefault(consts.ConfigKeyMirrorToken, "")
		mirrorTimeout := propertyManager.GetFloatWithDefault(consts.ConfigKeyMirrorTimeout, 0.0)
		mirrorLog := propertyManager.GetBoolWithDefault(consts.ConfigKeyMirrorEnableLog, false)
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
