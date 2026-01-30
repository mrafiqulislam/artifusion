package oci

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mainuli/artifusion/internal/config"
	"github.com/mainuli/artifusion/internal/proxy"
	"github.com/rs/zerolog"
)

func TestPrepareOCIHeaders_ContentLengthHandling(t *testing.T) {
	h := &Handler{
		config: &config.OCIConfig{},
		logger: zerolog.New(io.Discard),
	}
	backend := &config.OCIBackendConfig{}

	tests := []struct {
		name         string
		method       string
		path         string
		status       int
		wantPresent  bool
	}{
		{"blob get 200 keeps", http.MethodGet, "/v2/ns/repo/blobs/sha256:abc", http.StatusOK, true},
		{"blob get 404 removes", http.MethodGet, "/v2/ns/repo/blobs/sha256:abc", http.StatusNotFound, false},
		{"non-blob get removes", http.MethodGet, "/v2/ns/repo/manifests/latest", http.StatusOK, false},
		{"head keeps", http.MethodHead, "/v2/ns/repo/manifests/latest", http.StatusOK, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com"+tt.path, nil)
			resp := &proxy.Response{
				StatusCode: tt.status,
				Headers:    http.Header{"Content-Length": []string{"123"}},
			}

			h.prepareOCIHeaders(req, resp, backend)

			_, ok := resp.Headers["Content-Length"]
			if ok != tt.wantPresent {
				t.Fatalf("Content-Length present=%v, want %v", ok, tt.wantPresent)
			}
		})
	}
}
