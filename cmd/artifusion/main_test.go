package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mainuli/artifusion/internal/config"
	"github.com/mainuli/artifusion/internal/middleware"
)

func testConfig() *config.Config {
	return &config.Config{
		Protocols: config.ProtocolsConfig{
			Maven: config.MavenConfig{Enabled: true, PathPrefix: "/maven"},
			NPM:   config.NPMConfig{Enabled: true, PathPrefix: "/npm"},
		},
	}
}

func TestShouldSkipRequestTimeout(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"blob get", http.MethodGet, "/v2/ideascale/mysql-data/blobs/sha256:abc", true},
		{"blob head", http.MethodHead, "/v2/ideascale/mysql-data/blobs/sha256:abc", true},
		{"blob upload get", http.MethodGet, "/v2/ideascale/mysql-data/blobs/uploads/123", false},
		{"blob post", http.MethodPost, "/v2/ideascale/mysql-data/blobs/sha256:abc", false},
		{"blob path without digest", http.MethodGet, "/v2/ideascale/mysql-data/blobs/", false},
		{"blob path too short", http.MethodGet, "/v2/blobs/sha256:abc", false},
		{"non-blob get", http.MethodGet, "/v2/ideascale/mysql-data/manifests/latest", false},
		{"non-v2 blob path", http.MethodGet, "/other/blobs/sha256:abc", false},
		{"maven file get", http.MethodGet, "/maven/db-dumps/x/x.sql.gz", true},
		{"maven file head", http.MethodHead, "/maven/db-dumps/x/x.sql.gz", true},
		{"maven put keeps the timeout", http.MethodPut, "/maven/db-dumps/x/x.sql.gz", false},
		{"maven delete keeps the timeout", http.MethodDelete, "/maven/db-dumps/x/x.sql.gz", false},
		{"maven prefix lookalike", http.MethodGet, "/mavenx/file", false},
		{"npm tarball get", http.MethodGet, "/npm/left-pad/-/left-pad-1.3.0.tgz", true},
		{"npm put keeps the timeout", http.MethodPut, "/npm/left-pad", false},
		{"unrelated get", http.MethodGet, "/health", false},
		{"unrelated path with file name", http.MethodGet, "/other/big.sql.gz", false},
	}

	cfg := testConfig()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com"+tt.path, nil)
			if got := shouldSkipRequestTimeout(req, cfg); got != tt.want {
				t.Fatalf("shouldSkipRequestTimeout()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldSkipRequestTimeoutConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		path string
		want bool
	}{
		{"protocols disabled", &config.Config{}, "/maven/x/x.sql.gz", false},
		{"disabled protocol keeps its timeout", &config.Config{Protocols: config.ProtocolsConfig{
			Maven: config.MavenConfig{Enabled: false, PathPrefix: "/maven"}}}, "/maven/x/x.sql.gz", false},
		// host-based routing has no path prefix: an empty prefix must not exempt every path
		{"empty prefix exempts nothing", &config.Config{Protocols: config.ProtocolsConfig{
			Maven: config.MavenConfig{Enabled: true, Host: "maven.example.com"}}}, "/anything/else", false},
		{"trailing slash in prefix", &config.Config{Protocols: config.ProtocolsConfig{
			Maven: config.MavenConfig{Enabled: true, PathPrefix: "/maven/"}}}, "/maven/x/x.sql.gz", true},
		{"custom prefix", &config.Config{Protocols: config.ProtocolsConfig{
			Maven: config.MavenConfig{Enabled: true, PathPrefix: "/repo"}}}, "/repo/x/x.sql.gz", true},
		{"OCI blobs stay exempt", &config.Config{}, "/v2/a/blobs/sha256:abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.com"+tt.path, nil)
			if got := shouldSkipRequestTimeout(req, tt.cfg); got != tt.want {
				t.Fatalf("shouldSkipRequestTimeout()=%v, want %v", got, tt.want)
			}
		})
	}
}

// A slow Maven download must arrive complete, while any other request that
// outlives the timeout is still cut off. The chain is composed the same way
// main() does it.
func TestRequestTimeoutDoesNotCutArtifactDownloads(t *testing.T) {
	const chunks = 10
	const full = chunks * 1000
	cfg := testConfig()

	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			_, _ = io.WriteString(w, strings.Repeat("x", 1000))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(30 * time.Millisecond) // ~300ms response against a 50ms timeout
		}
	})

	timeoutMiddleware := middleware.Timeout(50 * time.Millisecond)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldSkipRequestTimeout(r, cfg) {
			slow.ServeHTTP(w, r)
			return
		}
		timeoutMiddleware(slow).ServeHTTP(w, r)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	read := func(path string) int {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return len(body)
	}

	if got := read("/maven/db-dumps/x/x.sql.gz"); got != full {
		t.Fatalf("maven download truncated: got %d bytes, want %d", got, full)
	}
	if got := read("/npm/pkg/-/pkg-1.0.0.tgz"); got != full {
		t.Fatalf("npm download truncated: got %d bytes, want %d", got, full)
	}
	if got := read("/v2/a/b/blobs/sha256:abc"); got != full {
		t.Fatalf("OCI blob download truncated: got %d bytes, want %d", got, full)
	}
	if got := read("/other/slow"); got >= full {
		t.Fatalf("non-artifact request was not subject to the timeout: got %d bytes", got)
	}
}
