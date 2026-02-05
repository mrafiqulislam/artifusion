package oci

import "testing"

func TestIsOCIBlobPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"valid blob path", "/v2/ns/repo/blobs/sha256:abc", true},
		{"valid blob path with nested name", "/v2/ns/repo/sub/blobs/sha256:abc", true},
		{"missing digest", "/v2/ns/repo/blobs/", false},
		{"too short", "/v2/blobs/sha256:abc", false},
		{"upload path", "/v2/ns/repo/blobs/uploads/123", false},
		{"non-v2 path", "/other/blobs/sha256:abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOCIBlobPath(tt.path); got != tt.want {
				t.Fatalf("IsOCIBlobPath(%q)=%v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
