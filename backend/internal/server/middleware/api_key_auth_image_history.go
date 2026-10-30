package middleware

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Only actual history routes skip monetary eligibility. Mutations and arbitrary
// /images paths still pass through ordinary gateway admission.
func isImageHistoryRead(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	const prefix = "/v1/images/history"
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix+"/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix+"/"), "/")
	if len(parts) != 1 && (len(parts) != 3 || parts[1] != "files") {
		return false
	}
	id, err := uuid.Parse(parts[0])
	return err == nil && id.String() == parts[0]
}
