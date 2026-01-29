package mooncake

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// ---- helpers for test server ----
func newBatchQueryServer(t *testing.T, data map[string][]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/batch_query_keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		keysStr := r.URL.Query().Get("keys")
		if keysStr == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"error":"No keys provided"}`))
			return
		}

		keys := strings.Split(keysStr, ",")
		resp := map[string]any{
			"success": true,
			"data":    map[string]any{},
		}
		dataObj := resp["data"].(map[string]any)

		// Return requested keys; each key maps to {ok, values:[{transport_endpoint_}]}
		for _, k := range keys {
			if eps, ok := data[k]; ok {
				arr := make([]map[string]any, 0, len(eps))
				for _, ep := range eps {
					arr = append(arr, map[string]any{
						"size_":               1024,
						"buffer_address_":     1234567890,
						"transport_endpoint_": ep,
					})
				}
				dataObj[k] = map[string]any{
					"ok":     true,
					"values": arr,
				}
			} else {
				dataObj[k] = map[string]any{
					"ok":     true,
					"values": []any{},
				}
			}
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func hostPortFromServerURL(t *testing.T, raw string) (host string, port int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	host = u.Hostname()
	p := u.Port()
	if p == "" {
		t.Fatalf("server url missing port: %s", raw)
	}
	_, err = fmt.Sscanf(p, "%d", &port)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return
}

// ---- tests ----
func TestBatchQueryPrefixHashHitKVSInstances(t *testing.T) {
	// Arrange: server data (key -> transport_endpoint_ list)
	serverData := map[string][]string{
		"mykey1": {"192.168.1.100:8080", "10.0.0.2:9999"},
		"mykey2": {"127.0.0.1:1234"},
	}
	srv := newBatchQueryServer(t, serverData)
	defer srv.Close()
	host, port := hostPortFromServerURL(t, srv.URL)
	c, err := NewMetadataServiceClient(host, port)
	if err != nil {
		t.Fatalf("NewMetadataServiceClient: %v", err)
	}
	// Note: your batchQueryKeys uses 5s timeout ctx internally; keep test fast anyway.
	_ = context.Background()
	_ = time.Second
	// Act
	got, err := c.BatchQueryPrefixHashHitKVSInstances([]string{"mykey1", "mykey2", "mykey3"})
	if err != nil {
		t.Fatalf("BatchQueryPrefixHashHitKVSInstances error: %v", err)
	}
	// Assert: values should be IP only (port stripped)
	want := map[string][]string{
		"mykey1": {"192.168.1.100", "10.0.0.2"},
		"mykey2": {"127.0.0.1"},
		"mykey3": {}, // not found -> empty list
	}
	// Compare in a simple way (order matters here, and server preserves order)
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Fatalf("missing key %q in result; got=%v", k, got)
		}
		if len(g) != len(w) {
			t.Fatalf("key %q: len mismatch got=%d want=%d, got=%v want=%v", k, len(g), len(w), g, w)
		}
		for i := range w {
			if g[i] != w[i] {
				t.Fatalf("key %q idx %d: got=%q want=%q (got=%v)", k, i, g[i], w[i], g)
			}
		}
	}
}

func TestBatchQueryKeys_HTTPError(t *testing.T) {
	// Server returns non-200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	host, port := hostPortFromServerURL(t, srv.URL)
	c, _ := NewMetadataServiceClient(host, port)
	_, err := c.BatchQueryPrefixHashHitKVSInstances([]string{"mykey1"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

// ---- HashTokens tests ----
func TestHashTokens_InvalidInput(t *testing.T) {
	c := &MetadataServiceClient{}
	_, err := c.HashTokens(nil, 4, true, "iris_", "vllm_")
	if err == nil {
		t.Fatalf("expected error for empty tokens")
	}
	_, err = c.HashTokens([]int64{1, 2, 3}, 0, true, "iris_", "vllm_")
	if err == nil {
		t.Fatalf("expected error for chunkSize<=0")
	}
}

func TestHashTokens_Chunking_SaveUnfullChunk(t *testing.T) {
	t.Setenv("MOONCAKE_HASH_SEED", "seed-1")
	c := &MetadataServiceClient{}
	tokens := []int64{1, 2, 3, 4, 5} // chunkSize=4 => 1 full + remainder
	hs1, err := c.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if len(hs1) != 2 {
		t.Fatalf("expected 2 hashes, got %d: %v", len(hs1), hs1)
	}
	hs2, err := c.HashTokens(tokens, 4, false, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if len(hs2) != 1 {
		t.Fatalf("expected 1 hash, got %d: %v", len(hs2), hs2)
	}
}

func TestHashTokens_DeterministicWithSameSeed(t *testing.T) {
	t.Setenv("MOONCAKE_HASH_SEED", "seed-xyz")
	c := &MetadataServiceClient{}
	tokens := []int64{10, 11, 12, 13}
	h1, err := c.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	h2, err := c.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if len(h1) != len(h2) || h1[0] != h2[0] {
		t.Fatalf("expected deterministic hashes, got h1=%v h2=%v", h1, h2)
	}
}

func TestHashTokens_DifferentSeedDifferentResult(t *testing.T) {
	c := &MetadataServiceClient{}
	tokens := []int64{10, 11, 12, 13}
	os.Setenv("MOONCAKE_HASH_SEED", "seed-a")
	h1, err := c.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	os.Setenv("MOONCAKE_HASH_SEED", "seed-b")
	h2, err := c.HashTokens(tokens, 4, true, "iris_", "vllm_")
	if err != nil {
		t.Fatalf("HashTokens err=%v", err)
	}
	if h1[0] == h2[0] {
		t.Fatalf("expected different hashes for different seeds, h1=%v h2=%v", h1, h2)
	}
}

// ---- BatchQuery response parsing tests ----
func TestBatchQueryPrefixHashHitKVSInstances_SuccessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"data":    map[string]any{},
		})
	}))
	defer srv.Close()
	host, port := hostPortFromServerURL(t, srv.URL)
	c, _ := NewMetadataServiceClient(host, port)
	_, err := c.BatchQueryPrefixHashHitKVSInstances([]string{"k1"})
	if err == nil {
		t.Fatalf("expected error when success=false")
	}
}

func TestBatchQueryPrefixHashHitKVSInstances_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success": true, "data":`)) // broken json
	}))
	defer srv.Close()
	host, port := hostPortFromServerURL(t, srv.URL)
	c, _ := NewMetadataServiceClient(host, port)
	_, err := c.BatchQueryPrefixHashHitKVSInstances([]string{"k1"})
	if err == nil {
		t.Fatalf("expected json decode error")
	}
}

func TestBatchQueryPrefixHashHitKVSInstances_EmptyTransportEndpointSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"k1": map[string]any{
					"ok": true,
					"values": []any{
						map[string]any{"transport_endpoint_": ""},
						map[string]any{"transport_endpoint_": "1.2.3.4:9999"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	host, port := hostPortFromServerURL(t, srv.URL)
	c, _ := NewMetadataServiceClient(host, port)

	got, err := c.BatchQueryPrefixHashHitKVSInstances([]string{"k1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got["k1"]) != 1 || got["k1"][0] != "1.2.3.4" {
		t.Fatalf("unexpected got=%v", got)
	}
}
