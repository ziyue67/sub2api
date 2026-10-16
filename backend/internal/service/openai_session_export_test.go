package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type sessionExportTransport func(*http.Request) (*http.Response, error)

func (f sessionExportTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func sessionExportAccount() *Account {
	return &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_user_id": "user-test", "email": "test@example.test", "access_token": "oauth-must-not-be-used"}}
}
func TestExportChatGPTSession(t *testing.T) {
	body := `{"user":{"id":"user-test","email":"test@example.test"},"accessToken":"web-token","expires":"2099-01-01T00:00:00Z","extraField":{"preserved":true}}`
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  string
	}{
		{"success", body, 200, ""},
		{"logged out", `{}`, 200, "logged-in Session"},
		{"html challenge", `<html>Just a moment</html>`, 200, "logged-in Session"},
		{"rejected", `secret upstream error must not leak`, 403, "HTTP 403"},
		{"redirect", body, 302, "HTTP 302"},
		{"different user", strings.ReplaceAll(body, "user-test", "other-user"), 200, "different user"},
		{"expired", strings.ReplaceAll(body, "2099-01-01", "2000-01-01"), 200, "expired"},
		{"oversized", strings.Repeat("x", (1<<20)+1), 200, "oversized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			svc := &OpenAIOAuthService{privacyClientFactory: func(proxy string) (*req.Client, error) {
				require.Empty(t, proxy)
				client := req.C()
				client.GetClient().Transport = sessionExportTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, chatGPTSessionURL, r.URL.String())
					require.Equal(t, "GET", r.Method)
					require.Equal(t, "__Secure-next-auth.session-token=test-cookie", r.Header.Get("Cookie"))
					require.Empty(t, r.Header.Get("Authorization"))
					deadline, ok := r.Context().Deadline()
					require.True(t, ok)
					require.WithinDuration(t, time.Now().Add(20*time.Second), deadline, time.Second)
					return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"https://example.test/"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
				})
				return client, nil
			}}
			account := sessionExportAccount()
			result, err := svc.ExportChatGPTSession(context.Background(), account, "__Secure-next-auth.session-token=test-cookie")
			require.Equal(t, 1, calls)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.NotContains(t, err.Error(), "secret upstream")
				require.Empty(t, result)
			} else {
				require.NoError(t, err)
				require.JSONEq(t, body, string(result))
			}
			require.NotContains(t, account.Credentials, "cookie")
		})
	}
}
func TestExportChatGPTSessionPreflight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account *Account
		cookie  string
		want    string
	}{
		{"missing cookie", sessionExportAccount(), "", "Cookie is required"},
		{"wrong cookie", sessionExportAccount(), "other=value", "Cookie must include"},
		{"newline", sessionExportAccount(), "__Secure-next-auth.session-token=x\r\nX: y", "Invalid"},
		{"large cookie", sessionExportAccount(), strings.Repeat("x", 16385), "Invalid"},
		{"nil account", nil, "x", "OpenAI OAuth"},
		{"api key", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "x", "OpenAI OAuth"},
		{"other platform", &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}, "x", "OpenAI OAuth"},
		{"shadow", &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: new(int64)}, "x", "OpenAI OAuth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIOAuthService{privacyClientFactory: func(string) (*req.Client, error) { t.Fatal("must not call upstream"); return nil, nil }}
			_, err := svc.ExportChatGPTSession(context.Background(), tc.account, tc.cookie)
			require.ErrorContains(t, err, tc.want)
		})
	}
}
func TestExportChatGPTSessionProxyFailsClosed(t *testing.T) {
	account := sessionExportAccount()
	account.ProxyID = new(int64)
	svc := &OpenAIOAuthService{privacyClientFactory: func(string) (*req.Client, error) { t.Fatal("must not fall back to direct"); return nil, nil }}
	_, err := svc.ExportChatGPTSession(context.Background(), account, "__Secure-next-auth.session-token.0=test")
	require.ErrorContains(t, err, "proxy is unavailable")
}
func TestExportChatGPTSessionTransportErrorIsSanitized(t *testing.T) {
	svc := &OpenAIOAuthService{privacyClientFactory: func(string) (*req.Client, error) {
		client := req.C()
		client.GetClient().Transport = sessionExportTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("secret Cookie in diagnostic") })
		return client, nil
	}}
	_, err := svc.ExportChatGPTSession(context.Background(), sessionExportAccount(), "__Secure-next-auth.session-token=x")
	require.ErrorContains(t, err, "failed or timed out")
	require.NotContains(t, err.Error(), "secret")
}

type sessionExportProxyRepo struct {
	ProxyRepository
	proxy *Proxy
	err   error
}

func (r *sessionExportProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	return r.proxy, r.err
}
func TestExportChatGPTSessionUsesProxyAndEmailFallback(t *testing.T) {
	account := sessionExportAccount()
	delete(account.Credentials, "chatgpt_user_id")
	account.ProxyID = new(int64)
	proxy := &Proxy{Protocol: "http", Host: "127.0.0.1", Port: 1234}
	for _, tc := range []struct {
		name, email string
		wantError   bool
	}{
		{"matching email", "TEST@example.test", false}, {"different email", "other@example.test", true}, {"no email", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIOAuthService{proxyRepo: &sessionExportProxyRepo{proxy: proxy}, privacyClientFactory: func(proxyURL string) (*req.Client, error) {
				require.Equal(t, proxy.URL(), proxyURL)
				client := req.C().ImpersonateFirefox()
				client.GetClient().Transport = sessionExportTransport(func(r *http.Request) (*http.Response, error) {
					require.NotEmpty(t, r.UserAgent())
					require.Equal(t, "__Secure-next-auth.session-token.0=first; __Secure-next-auth.session-token.1=second", r.Header.Get("Cookie"))
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"user":{"id":"user-test","email":"` + tc.email + `"},"accessToken":"web-token","expires":"2099-01-01T00:00:00Z"}`))}, nil
				})
				return client, nil
			}}
			_, err := svc.ExportChatGPTSession(context.Background(), account, "__Secure-next-auth.session-token.0=first; __Secure-next-auth.session-token.1=second")
			if tc.wantError {
				require.ErrorContains(t, err, "Cannot match")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
