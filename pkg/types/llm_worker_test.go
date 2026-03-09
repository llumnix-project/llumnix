package types

import (
	"testing"
)

// ---------------------------------------------------------------------------
// hasVersionSuffix
// ---------------------------------------------------------------------------

func TestHasVersionSuffix(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		// positive cases
		{"https://ark.cn-beijing.volces.com/api/v3", true},
		{"http://example.com/v1", true},
		{"http://example.com/v10", true},
		// negative cases
		{"https://example.com/api/v3/chat", false}, // version is not the last segment
		{"https://example.com/api/v", false},       // "/v" with no digits
		{"https://example.com/api", false},         // no version at all
		{"https://example.com/version3", false},    // "version" not "/v"
		{"", false},
	}

	for _, c := range cases {
		got := hasVersionSuffix(c.input)
		if got != c.want {
			t.Errorf("hasVersionSuffix(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// trimVersionPrefix
// ---------------------------------------------------------------------------

func TestTrimVersionPrefix(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/v1/chat/completions", "/chat/completions"},
		{"/v10/completions", "/completions"},
		{"/v1", "/"},  // only version segment, strip to "/"
		{"/v1/", "/"}, // trailing slash after version
	}

	for _, c := range cases {
		got := trimVersionPrefix(c.input)
		if got != c.want {
			t.Errorf("trimVersionPrefix(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// BuildRequestURL
// ---------------------------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		path    string
		want    string
		wantErr bool
	}{
		{
			name:    "base with version, path with version - strip path version",
			baseURL: "https://ark.cn-beijing.volces.com/api/v3",
			path:    "/v1/chat/completions",
			want:    "https://ark.cn-beijing.volces.com/api/v3/chat/completions",
		},
		{
			name:    "base with version, path without version - keep as-is",
			baseURL: "https://ark.cn-beijing.volces.com/api/v3",
			path:    "/chat/completions",
			want:    "https://ark.cn-beijing.volces.com/api/v3/chat/completions",
		},
		{
			name:    "base without version, path with version - keep path version",
			baseURL: "http://example.com",
			path:    "/v1/chat/completions",
			want:    "http://example.com/v1/chat/completions",
		},
		{
			name:    "base with trailing slash is trimmed",
			baseURL: "https://ark.cn-beijing.volces.com/api/v3/",
			path:    "/v1/completions",
			want:    "https://ark.cn-beijing.volces.com/api/v3/completions",
		},
		{
			name:    "base without version, plain path",
			baseURL: "http://example.com",
			path:    "/completions",
			want:    "http://example.com/completions",
		},
		{
			name:    "base with high version number",
			baseURL: "https://example.com/api/v10",
			path:    "/v1/chat/completions",
			want:    "https://example.com/api/v10/chat/completions",
		},
		{
			name:    "both BaseURL and Endpoint empty - returns error",
			baseURL: "",
			path:    "/v1/chat/completions",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &LLMWorker{BaseURL: c.baseURL}
			got, err := w.BuildRequestURL(c.path)
			if c.wantErr {
				if err == nil {
					t.Errorf("BuildRequestURL(%q) expected error, got nil", c.path)
				}
				return
			}
			if err != nil {
				t.Errorf("BuildRequestURL(%q) unexpected error: %v", c.path, err)
				return
			}
			if got != c.want {
				t.Errorf("BuildRequestURL(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BuildRequestURL - Endpoint branch
// ---------------------------------------------------------------------------

func TestBuildRequestURL_Endpoint(t *testing.T) {
	cases := []struct {
		name     string
		endpoint Endpoint
		path     string
		want     string
	}{
		{
			name:     "endpoint takes priority over empty BaseURL",
			endpoint: Endpoint{Host: "192.168.0.1", Port: 8080},
			path:     "/v1/chat/completions",
			want:     "http://192.168.0.1:8080/v1/chat/completions",
		},
		{
			name:     "endpoint with domain",
			endpoint: Endpoint{Host: "worker.internal", Port: 9000},
			path:     "/completions",
			want:     "http://worker.internal:9000/completions",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &LLMWorker{Endpoint: c.endpoint}
			got, err := w.BuildRequestURL(c.path)
			if err != nil {
				t.Errorf("BuildRequestURL(%q) unexpected error: %v", c.path, err)
				return
			}
			if got != c.want {
				t.Errorf("BuildRequestURL(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}
