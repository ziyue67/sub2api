package config

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// Empty keeps the shared legacy setting. An override is trusted per-process
// configuration, never an origin derived from a client's Host/forwarded headers.
func ValidateImageRelayOwnerOrigin(raw string) error {
	if raw == "" {
		return nil
	}
	if err := urlvalidator.ValidateHTTPSOrigin(raw); err != nil {
		return fmt.Errorf("runtime.image_relay_owner_origin requires an HTTPS origin without credentials, path, query or fragment")
	}
	return nil
}
