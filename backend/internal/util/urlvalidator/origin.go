package urlvalidator

import (
	"errors"
	"net/url"
	"strings"
)

// ValidateHTTPSOrigin is syntax-only and never performs DNS or network I/O.
// Error text never echoes potentially credential-bearing caller input.
func ValidateHTTPSOrigin(raw string) error {
	invalid := errors.New("HTTPS origin must not contain credentials, path, query or fragment and must use a valid port")
	// Reuse the existing syntax-only port validation without propagating its
	// input-bearing diagnostics. Do not use DNS/allowlist validation here.
	if _, err := ValidateURLFormat(raw, false); err != nil {
		return invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return invalid
	}
	return nil
}
