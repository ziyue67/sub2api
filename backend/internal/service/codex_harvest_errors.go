package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// Only controlled categories may cross the admin/log boundary. Never retain an
// upstream error string: it may contain a URL, proxy credentials or response data.
type codexMintError struct {
	kind, detail string
	terminal     bool
}

// Only allowlisted codes cross the diagnostic boundary; messages can contain credentials.
func codex780EventError(raw []byte, eventName string) error {
	type upstreamError struct {
		Code   string `json:"code"`
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	var event struct {
		Type     string        `json:"type"`
		Status   int           `json:"status"`
		Error    upstreamError `json:"error"`
		Response struct {
			Error upstreamError `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil || (event.Type != "error" && event.Type != "response.failed") || (eventName != "" && eventName != event.Type) {
		return nil
	}
	e := event.Error
	if event.Type == "response.failed" {
		e = event.Response.Error
	}
	code := e.Code
	if code == "" {
		code = e.Type
	}
	kind, terminal := "response_incomplete_or_error", false
	switch code {
	case "authentication_error", "invalid_api_key", "permission_denied":
		kind, terminal = "account_error", true
	case "rate_limit_exceeded", "rate_limit_error":
		kind = "rate_limited"
	case "invalid_request_error", "invalid_request", "invalid_argument", "model_not_found", "unsupported_model", "invalid_model", "insufficient_quota":
		terminal = true
	default:
		code = "unknown"
	}
	status := event.Status
	if status == 0 {
		status = e.Status
	}
	if status == 401 || status == 403 {
		kind, terminal = "account_error", true
	} else if status == 429 && kind != "account_error" {
		kind = "rate_limited"
	} else if status == 400 || status == 404 || status == 422 {
		terminal = true
	}
	return &codexMintError{kind: kind, detail: "mint error event: " + code, terminal: terminal}
}

func codex780TerminalFailure(result codexHarvestProbeResult) bool {
	var err *codexMintError
	return result.Status == 400 || result.Status == 404 || result.Status == 422 || (errors.As(result.Err, &err) && err.terminal)
}

func (e *codexMintError) Error() string { return e.detail }

func mintRouteError(detail string) error {
	return &codexMintError{kind: "invalid_route", detail: detail}
}

func mintTransportError(err error) error {
	var safe *codexMintError
	if errors.As(err, &safe) {
		return safe
	}
	category := "transport_error"
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		category = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		category = "timeout"
	case errors.As(err, &dns):
		category = "dns_error"
	case errors.As(err, &certificate), errors.As(err, &unknownCA), errors.As(err, &hostname):
		category = "tls_certificate_error"
	case errors.Is(err, syscall.ECONNREFUSED):
		category = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		category = "connection_closed"
	case errors.As(err, &netErr) && netErr.Timeout():
		category = "timeout"
	case err != nil && strings.Contains(strings.ToLower(err.Error()), "tls:"):
		category = "tls_error"
	}
	return &codexMintError{kind: "network_error", detail: "mint transport: " + category}
}

func safeCodexHarvestError(err error) string {
	if err == nil {
		return ""
	}
	var safe *codexMintError
	if errors.As(err, &safe) {
		return safe.Error()
	}
	return mintTransportError(err).Error()
}
