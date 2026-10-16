package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const chatGPTSessionURL = "https://chatgpt.com/api/auth/session"

// ExportChatGPTSession fetches a real web session. OAuth tokens are not web
// cookies; the caller supplies a temporary Cookie header, which is never saved.
func (s *OpenAIOAuthService) ExportChatGPTSession(ctx context.Context, account *Account, cookie string) (json.RawMessage, error) {
	fail := func(status int, code, message string) (json.RawMessage, error) {
		return nil, infraerrors.New(status, code, message)
	}
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.ParentAccountID != nil {
		return fail(http.StatusBadRequest, "OPENAI_SESSION_ACCOUNT_UNSUPPORTED", "Session export requires an OpenAI OAuth account with its own credentials")
	}
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return fail(http.StatusBadRequest, "OPENAI_SESSION_COOKIE_REQUIRED", "ChatGPT login Cookie is required; OAuth access tokens cannot replace it")
	}
	if len(cookie) > 16384 || strings.ContainsAny(cookie, "\r\n") {
		return fail(http.StatusBadRequest, "OPENAI_SESSION_COOKIE_INVALID", "Invalid ChatGPT login Cookie")
	}
	parsed := (&http.Request{Header: http.Header{"Cookie": []string{cookie}}}).Cookies()
	hasSession := false
	for _, c := range parsed {
		if c.Value != "" && (c.Name == "__Secure-next-auth.session-token" || strings.HasPrefix(c.Name, "__Secure-next-auth.session-token.")) {
			hasSession = true
		}
	}
	if !hasSession {
		return fail(http.StatusBadRequest, "OPENAI_SESSION_COOKIE_REQUIRED", "Cookie must include __Secure-next-auth.session-token (or its numbered chunks)")
	}
	if s == nil || s.privacyClientFactory == nil {
		return fail(http.StatusServiceUnavailable, "OPENAI_SESSION_UNAVAILABLE", "Session export is unavailable")
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if s.proxyRepo == nil {
			return fail(http.StatusServiceUnavailable, "OPENAI_SESSION_PROXY_UNAVAILABLE", "Account proxy is unavailable")
		}
		proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID)
		if err != nil || proxy == nil {
			return fail(http.StatusServiceUnavailable, "OPENAI_SESSION_PROXY_UNAVAILABLE", "Account proxy is unavailable")
		}
		proxyURL = proxy.URL()
	}
	client, err := s.privacyClientFactory(proxyURL)
	if err != nil || client == nil {
		return fail(http.StatusBadGateway, "OPENAI_SESSION_CLIENT_FAILED", "Could not create the Session export client")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, chatGPTSessionURL, nil)
	if err != nil {
		return fail(http.StatusInternalServerError, "OPENAI_SESSION_REQUEST_FAILED", "Could not create the Session request")
	}
	request.Header = client.Headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Del("Authorization")
	request.Header.Set("Cookie", cookie)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Del("Sec-Fetch-User")
	request.Header.Del("Upgrade-Insecure-Requests")
	request.Header.Set("Referer", "https://chatgpt.com/")
	// Keep credentials on the fixed origin and do not share the factory's cookie jar.
	httpClient := *client.GetClient()
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(request)
	if err != nil {
		return fail(http.StatusBadGateway, "OPENAI_SESSION_REQUEST_FAILED", "ChatGPT Session request failed or timed out")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fail(http.StatusBadGateway, "OPENAI_SESSION_UPSTREAM_REJECTED", "ChatGPT Session request returned HTTP "+strconv.Itoa(resp.StatusCode)+"; check login Cookie and account proxy")
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return fail(http.StatusBadGateway, "OPENAI_SESSION_INVALID_RESPONSE", "ChatGPT returned an unreadable or oversized Session response")
	}
	var session struct {
		AccessToken string `json:"accessToken"`
		Expires     string `json:"expires"`
		User        struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if json.Unmarshal(body, &session) != nil || strings.TrimSpace(session.AccessToken) == "" {
		return fail(http.StatusBadGateway, "OPENAI_SESSION_LOGIN_REQUIRED", "ChatGPT did not return a logged-in Session; renew the login Cookie or check for a browser challenge")
	}
	expires, err := time.Parse(time.RFC3339, session.Expires)
	if err != nil || !expires.After(time.Now()) {
		return fail(http.StatusBadGateway, "OPENAI_SESSION_EXPIRED", "ChatGPT returned an expired or invalid Session")
	}
	userID, email := account.GetCredential("chatgpt_user_id"), account.GetCredential("email")
	if userID != "" {
		if session.User.ID != userID {
			return fail(http.StatusBadRequest, "OPENAI_SESSION_IDENTITY_MISMATCH", "ChatGPT login Cookie belongs to a different user")
		}
	} else if email == "" || !strings.EqualFold(session.User.Email, email) {
		return fail(http.StatusBadRequest, "OPENAI_SESSION_IDENTITY_MISMATCH", "Cannot match the ChatGPT Session to this account; check login Cookie and account identity")
	}
	return json.RawMessage(body), nil
}
