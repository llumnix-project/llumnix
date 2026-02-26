package mooncake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"llumnix/pkg/types"
)

type MetadataServiceClient struct {
	metadataServiceClient *http.Client
	metadataServiceHost   string
	metadataServicePort   int
}

func NewMetadataServiceClient(metadataServiceHost string, metadataServicePort int) (*MetadataServiceClient, error) {
	httpClient := &http.Client{Timeout: 3 * time.Second}
	return &MetadataServiceClient{
		metadataServiceClient: httpClient,
		metadataServiceHost:   metadataServiceHost,
		metadataServicePort:   metadataServicePort,
	}, nil
}

func (c *MetadataServiceClient) BatchQueryPrefixHashHitKVSInstances(hashKeys []string) (map[string][]string, error) {
	resp, err := c.batchQueryKeys(hashKeys)
	if err != nil {
		return nil, fmt.Errorf("failed to query hit kvs instances for %d hash key: %w", len(hashKeys), err)
	}
	defer resp.Body.Close()
	hitKVSInstances, err := c.parseResponseToHitMap(resp)
	if err != nil {
		return nil, fmt.Errorf("batch query keys parse response failed: %v", err)
	}
	return hitKVSInstances, nil
}

func (c *MetadataServiceClient) batchQueryKeys(keys []string) (*http.Response, error) {
	endpoint := types.Endpoint{
		Host: c.metadataServiceHost,
		Port: c.metadataServicePort,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	newFunc := func() (*http.Request, error) {
		newReq, err := c.makeNewHttpRequest("GET", &endpoint, "/batch_query_keys", keys, ctx)
		if err != nil {
			return nil, fmt.Errorf("make http request failed: %v", err)
		}
		return newReq, nil
	}
	resp, _, err := c.doHttpRequest(newFunc)
	if err != nil {
		return nil, fmt.Errorf("batch query keys request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("batch query keys failed, http status=%s body=%s", resp.Status, string(b))
	}
	return resp, nil
}

func (c *MetadataServiceClient) makeNewHttpRequest(
	method string, endpoint *types.Endpoint, path string, keys []string, ctx context.Context,
) (req *http.Request, err error) {
	u := &url.URL{
		Scheme: "http",
		Host:   endpoint.String(),
		Path:   path,
	}

	escaped := make([]string, 0, len(keys))
	for _, k := range keys {
		escaped = append(escaped, url.QueryEscape(k))
	}
	u.RawQuery = "keys=" + strings.Join(escaped, ",")

	var newReq *http.Request
	if ctx != nil {
		newReq, err = http.NewRequestWithContext(ctx, method, u.String(), nil)
	} else {
		newReq, err = http.NewRequest(method, u.String(), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("make new http request %s:%s error: %s", method, u, err)
	}
	newReq.Header.Set("Accept", "application/json")
	return newReq, nil
}

func (c *MetadataServiceClient) doHttpRequest(newFunc func() (*http.Request, error)) (*http.Response, *http.Request, error) {
	nRetry := 0
RETRY:
	httpReq, err := newFunc()
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.metadataServiceClient.Do(httpReq)
	if err != nil {
		klog.Warningf("request to service %s error: %v, retry: %v", httpReq.URL.String(), err, nRetry)
		// if the error is not timeout, will retry
		var netErr net.Error
		if !(errors.As(err, &netErr) && netErr.Timeout()) {
			if nRetry < 3 {
				nRetry++
				time.Sleep(1 * time.Millisecond)
				goto RETRY
			}
		}
		return nil, nil, err
	}
	return resp, httpReq, nil
}

type BatchQueryKeysResponse struct {
	Success bool `json:"success"`
	Data    map[string]struct {
		OK     bool `json:"ok"`
		Values []struct {
			TransportEndpoint string `json:"transport_endpoint_"`
		} `json:"values"`
	} `json:"data"`
}

func (c *MetadataServiceClient) parseResponseToHitMap(resp *http.Response) (map[string][]string, error) {
	var decoded BatchQueryKeysResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if !decoded.Success {
		return nil, fmt.Errorf("batch query keys parse response failed")
	}
	out := make(map[string][]string, len(decoded.Data))
	for key, item := range decoded.Data {
		klog.V(4).Infof("batch query keys parse response key=%s, item=%v", key, item)
		ips := make([]string, 0, len(item.Values))
		for _, v := range item.Values {
			if v.TransportEndpoint == "" {
				continue
			}
			ips = append(ips, c.parseToKVSInstanceIp(v.TransportEndpoint))
		}
		out[key] = ips
	}
	return out, nil
}

func (c *MetadataServiceClient) parseToKVSInstanceIp(kvsInstance string) string {
	return strings.Split(kvsInstance, ":")[0]
}
