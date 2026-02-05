package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com"+tt.path, nil)
			if got := shouldSkipRequestTimeout(req); got != tt.want {
				t.Fatalf("shouldSkipRequestTimeout()=%v, want %v", got, tt.want)
			}
		})
	}
}
