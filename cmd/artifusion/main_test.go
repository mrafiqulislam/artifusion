package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mainuli/artifusion/internal/config"
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
		{"maven jar get", http.MethodGet, "/maven/com/example/app/1.0.0/app-1.0.0.jar", true},
		{"maven put keeps the timeout", http.MethodPut, "/maven/db-dumps/x/x.sql.gz", false},
		{"maven delete keeps the timeout", http.MethodDelete, "/maven/db-dumps/x/x.sql.gz", false},
		{"maven prefix lookalike", http.MethodGet, "/mavenx/file", false},
		{"npm tarball get", http.MethodGet, "/npm/left-pad/-/left-pad-1.3.0.tgz", true},
		{"npm put keeps the timeout", http.MethodPut, "/npm/left-pad", false},
		{"unrelated get", http.MethodGet, "/health", false},
		{"unrelated path with file name", http.MethodGet, "/other/big.sql.gz", false},
	}

	skip := newSkipRequestTimeout(testConfig())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com"+tt.path, nil)
			if got := skip(req); got != tt.want {
				t.Fatalf("skip(%s %s)=%v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestShouldSkipRequestTimeoutDisabledProtocols(t *testing.T) {
	skip := newSkipRequestTimeout(&config.Config{})
	for _, path := range []string{"/maven/db-dumps/x/x.sql.gz", "/npm/left-pad/-/left-pad-1.3.0.tgz"} {
		req := httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil)
		if skip(req) {
			t.Fatalf("skip(GET %s)=true although the protocol is disabled", path)
		}
	}
	blob := httptest.NewRequest(http.MethodGet, "http://example.com/v2/a/blobs/sha256:abc", nil)
	if !skip(blob) {
		t.Fatal("OCI blob downloads must stay exempt")
	}
}

// slowBody writes chunks with a pause between them, so the response lasts
// chunks*pause, longer than the timeout used in the test below.
func slowBody(chunks int, pause time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			_, _ = io.WriteString(w, strings.Repeat("x", 1000))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(pause)
		}
	})
}

// A slow Maven download must arrive complete, while any other request that
// outlives the timeout is still cut off.
func TestRequestTimeoutMiddlewareDoesNotCutArtifactDownloads(t *testing.T) {
	const chunks = 10
	const full = chunks * 1000
	handler := requestTimeoutMiddleware(50*time.Millisecond, newSkipRequestTimeout(testConfig()))(
		slowBody(chunks, 30*time.Millisecond)) // ~300ms response, 50ms timeout

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
