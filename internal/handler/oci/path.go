package oci

import "strings"

// IsOCIBlobPath returns true if the path matches /v2/<name>/blobs/<digest>.
// <name> may include slashes.
func IsOCIBlobPath(path string) bool {
	if !strings.HasPrefix(path, "/v2/") {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 5 { // ["", "v2", <name...>, "blobs", <digest>]
		return false
	}
	return parts[len(parts)-2] == "blobs" && parts[len(parts)-1] != ""
}
