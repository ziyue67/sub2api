package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const prismBrowserMaxResponseBytes = 2 << 20

func prismBrowserTerminal(body []byte, model string, stream bool) (string, error) {
	terminal := body
	if stream {
		terminal = nil
		for _, line := range bytes.Split(body, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if !gjson.ValidBytes(data) {
				return "", errors.New("prism adapter returned invalid SSE JSON")
			}
			switch gjson.GetBytes(data, "type").String() {
			case "response.created":
			case "response.completed":
				if terminal != nil {
					return "", errors.New("prism adapter returned repeated terminal events")
				}
				terminal = []byte(gjson.GetBytes(data, "response").Raw)
			default:
				return "", errors.New("prism adapter returned unsupported SSE event")
			}
		}
	}
	if !gjson.ValidBytes(terminal) || gjson.GetBytes(terminal, "status").String() != "completed" ||
		gjson.GetBytes(terminal, "model").String() != model ||
		gjson.GetBytes(terminal, "output.0.content.0.text").String() == "" ||
		gjson.GetBytes(terminal, "id").String() == "" {
		return "", errors.New("prism adapter returned an invalid terminal response")
	}
	return gjson.GetBytes(terminal, "id").String(), nil
}

func prismBrowserAdapterURL(baseURL string) (string, error) {
	parsed, err := url.Parse(prismBrowserResponsesURL(baseURL))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", errors.New("prism adapter must use a local HTTP endpoint")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip == nil || (!ip.Equal(net.ParseIP("127.0.0.1")) && !ip.Equal(net.IPv6loopback)) {
		return "", errors.New("prism adapter must bind to a numeric loopback address")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || parsed.Path != "/v1/responses" {
		return "", errors.New("invalid Prism adapter endpoint")
	}
	return parsed.String(), nil
}

// prismBrowserAdapterMisconfigured reports the adapter's own authentication and
// routing failures: the gateway and adapter disagree on the bridge key or path.
// Passing those statuses through would tell the client its API key was rejected.
func prismBrowserAdapterMisconfigured(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
		return true
	}
	return false
}

// prismBrowserAdapterErrorMessage tells an admin why the adapter refused a test
// turn (unsupported model, busy browser, retained pending turn). The adapter only
// returns fixed error codes and messages, never credentials or prompt text.
func prismBrowserAdapterErrorMessage(status int, body []byte) string {
	code := strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	message := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	switch {
	case code != "" && message != "":
		return fmt.Sprintf("Prism adapter returned HTTP %d (%s): %s", status, code, truncateString(message, 300))
	case code != "":
		return fmt.Sprintf("Prism adapter returned HTTP %d (%s)", status, code)
	default:
		return fmt.Sprintf("Prism adapter returned HTTP %d", status)
	}
}

func (s *OpenAIGatewayService) forwardPrismBrowser(ctx context.Context, c *gin.Context, account *Account, body []byte, started time.Time) (*OpenAIForwardResult, error) {
	if isOpenAIResponsesCompactPath(c) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Prism adapter does not support responses/compact"}})
		return nil, errors.New("prism adapter does not support responses/compact")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	stream := gjson.GetBytes(body, "stream").Bool()
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "model is required"}})
		return nil, errors.New("prism adapter model is required")
	}
	sessionID, err := prismBrowserSessionID(c, account.ID, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": err.Error()}})
		return nil, err
	}
	responseBody, upstreamHeaders, status, err := s.callPrismBrowserWithSession(ctx, account, body, sessionID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "prism_unavailable", "message": "Prism adapter unavailable; request was not replayed"}})
		return nil, err
	}
	if status != http.StatusOK {
		if prismBrowserAdapterMisconfigured(status) {
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "prism_unavailable", "message": "Prism adapter rejected the gateway; check the adapter key and endpoint"}})
			return nil, fmt.Errorf("prism adapter returned HTTP %d", status)
		}
		c.Data(status, "application/json", responseBody)
		return nil, fmt.Errorf("prism adapter returned HTTP %d", status)
	}
	responseID, err := prismBrowserTerminal(responseBody, model, stream)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "invalid_prism_response", "message": "Prism adapter returned no valid terminal response"}})
		return nil, err
	}
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	SetActualOpenAIUpstreamEndpoint(c, "/v1/responses")
	c.Header("X-Prism-Usage", "unavailable")
	c.Data(http.StatusOK, contentType, responseBody)
	return &OpenAIForwardResult{
		RequestID:        responseID,
		ResponseID:       responseID,
		UpstreamHeaders:  upstreamHeaders,
		Model:            model,
		UpstreamModel:    model,
		Stream:           stream,
		Duration:         time.Since(started),
		UsageUnavailable: true,
	}, nil
}

func (s *OpenAIGatewayService) callPrismBrowser(ctx context.Context, account *Account, body []byte) ([]byte, http.Header, int, error) {
	// Admin account tests always use a fresh project, even when a client sends
	// a session header. A previous answer must not contaminate a capability test.
	return s.callPrismBrowserWithSession(ctx, account, body, "")
}

func (s *OpenAIGatewayService) callPrismBrowserWithSession(ctx context.Context, account *Account, body []byte, sessionID string) ([]byte, http.Header, int, error) {
	if !accountUsesPrismBrowser(account, s.cfg) {
		return nil, nil, 0, errors.New("prism adapter is disabled; native fallback is prohibited")
	}
	endpoint, err := prismBrowserAdapterURL(s.cfg.Gateway.PrismBrowser.BaseURL)
	if err != nil {
		return nil, nil, 0, err
	}
	key := strings.TrimSpace(s.cfg.Gateway.PrismBrowser.APIKey)
	if key == "" {
		return nil, nil, 0, errors.New("prism adapter key is not configured")
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, nil, 0, err
	}
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, nil, 0, errors.New("invalid Prism OAuth token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-Prism-Account-ID", strconv.FormatInt(account.ID, 10))
	req.Header.Set("X-Prism-OAuth-Token", token)
	if sessionID != "" {
		req.Header.Set("X-Prism-Session-ID", sessionID)
	}
	// The token must never pass through an account proxy, environment proxy,
	// plugin transport, or an HTTP redirect.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:       5 * time.Minute,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("prism adapter request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, prismBrowserMaxResponseBytes+1))
	if err != nil || len(responseBody) > prismBrowserMaxResponseBytes {
		return nil, nil, 0, errors.New("prism adapter response exceeded limit")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, nil, 0, errors.New("prism adapter redirected unexpectedly")
	}
	return responseBody, resp.Header, resp.StatusCode, nil
}

// prismBrowserSessionID uses identities already sent by unmodified Codex clients.
// A bare API key identifies a tenant, not a conversation: missing identity must
// create a fresh project so unrelated requests cannot inherit project files.
func prismBrowserSessionID(c *gin.Context, accountID int64, body []byte) (string, error) {
	if c == nil || c.Request == nil {
		return "", nil
	}
	for _, names := range [][]string{openAIThreadIdentityHeaders, openAISessionIdentityHeaders} {
		for _, name := range names {
			if len(c.Request.Header.Values(name)) > 1 {
				return "", errors.New("prism conversation identity headers must not be repeated")
			}
		}
	}
	resolution := resolveOpenAIClientSessionIdentity(c, body)
	switch resolution.metadata.Status {
	case OpenAIClientSessionIdentityMissing:
		return "", nil
	case OpenAIClientSessionIdentityResolved:
	default:
		return "", errors.New("prism conversation identity is invalid or conflicting")
	}
	keyID := getAPIKeyIDFromContext(c)
	if keyID <= 0 || accountID <= 0 {
		return "", errors.New("prism session reuse requires an authenticated API key")
	}
	// The private adapter header is always derived here; client-supplied
	// X-Prism-Session-ID values cannot select an existing cached context.
	digest := sha256.Sum256([]byte(fmt.Sprintf("prism-session-v1:%d:%d:%s:%s",
		keyID, accountID, resolution.identity.kind, resolution.identity.value)))
	return hex.EncodeToString(digest[:]), nil
}
