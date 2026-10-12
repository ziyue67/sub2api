//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type accountTicketUpstream struct {
	do func(*http.Request, string) (*http.Response, error)
}

func (u *accountTicketUpstream) Do(r *http.Request, p string, _ int64, _ int) (*http.Response, error) {
	return u.do(r, p)
}
func (u *accountTicketUpstream) DoWithTLS(r *http.Request, p string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.do(r, p)
}

// Exercise the real test request builder, not only the ticket injection helper.
// This account deliberately is not a gateway-reuse target.
func TestAccountTestStoredTicketBundle(t *testing.T) {
	for _, mode := range []string{"default", "pelican", "compact"} {
		t.Run(mode, func(t *testing.T) {
			account := ticketTestAccount(71)
			account.Credentials["model_mapping"] = map[string]any{"test-alias": "gpt-6-astra"}
			now := time.Now().Truncate(time.Second)
			ticket := &openAICodexTicket{AccountID: account.ID, Model: "gpt-6-astra", State: mint780State(now), Length: 780, ProbeVerified: true, Transport: "sse", Gateway: "unified-42", IssuedAt: now, CapturedAt: now, ExpiresAt: now.Add(240 * time.Second), HarvestCookies: mint780Pair(now.Add(time.Hour), "unified-42"), HarvestProxyURL: "http://ticket.invalid:8080"}
			gateway := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 780, Models: []string{"gpt-6-astra"}}, nil)
			require.NoError(t, gateway.storeOpenAICodexTicket(t.Context(), account, ticket))
			calls := 0
			upstream := &accountTicketUpstream{do: func(req *http.Request, proxy string) (*http.Response, error) {
				calls++
				require.Equal(t, ticket.State, req.Header.Get(openAICodexTurnStateHeader))
				require.Equal(t, strings.Join(ticket.HarvestCookies, "; "), req.Header.Get("Cookie"))
				require.Equal(t, ticket.HarvestProxyURL, proxy)
				require.Equal(t, "Bearer tok", req.Header.Get("Authorization"))
				require.True(t, gateway.codexTicketChatHeld(account.ID))
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
				if mode == "pelican" {
					require.Contains(t, string(body), "synthetic quality prompt")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(harvestProbeCompleteFixture("gpt-6-astra")))}, nil
			}}
			gateway.httpUpstream = upstream
			svc := &AccountTestService{openaiGatewayService: gateway, httpUpstream: upstream}
			ctx, _ := newTestContext()
			testMode := AccountTestModeDefault
			if mode == "pelican" {
				ctx.Request = ctx.Request.WithContext(withPelicanTestOptions(ctx.Request.Context(), pelicanTestOptions{prompt: "synthetic quality prompt", reasoningEffort: "high"}))
			}
			if mode == "compact" {
				testMode = AccountTestModeCompact
			}
			// Compact response shape is separately tested; this assertion targets the outbound bundle.
			err := svc.testOpenAIAccountConnection(ctx, account, "test-alias", "", testMode)
			if mode != "compact" {
				require.NoError(t, err)
			}
			require.Equal(t, 1, calls)
			require.False(t, gateway.codexTicketChatHeld(account.ID), "closing the stream releases the harvest guard")
		})
	}
}

func accountTestTicketFixture(t *testing.T) (*AccountTestService, *Account, *openAICodexTicket) {
	t.Helper()
	account := ticketTestAccount(71)
	gateway := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 780, Models: []string{"gpt-6-astra"}}, nil)
	now := time.Now().Truncate(time.Second)
	ticket := &openAICodexTicket{AccountID: account.ID, Model: "gpt-6-astra", State: mint780State(now), Length: 780, ProbeVerified: true, Transport: "sse", Gateway: "unified-42", IssuedAt: now, CapturedAt: now, ExpiresAt: now.Add(240 * time.Second), HarvestCookies: mint780Pair(now.Add(time.Hour), "unified-42"), HarvestProxyURL: "http://ticket.invalid:8080"}
	require.NoError(t, gateway.storeOpenAICodexTicket(t.Context(), account, ticket))
	return &AccountTestService{openaiGatewayService: gateway}, account, ticket
}

func TestAccountTestStoredTicketPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		closed, wantTicket, wantError bool
	}{
		{name: "valid", wantTicket: true}, {name: "persisted", wantTicket: true}, {name: "revoked"}, {name: "unverified"}, {name: "acquisition"}, {name: "disabled"}, {name: "other_model"}, {name: "api_key"}, {name: "other_endpoint"},
		{name: "missing"}, {name: "missing", closed: true, wantError: true},
		{name: "expired"}, {name: "expired", closed: true, wantError: true},
		{name: "websocket"}, {name: "websocket", closed: true, wantError: true},
	} {
		name := tc.name
		if tc.closed {
			name += "_fail_closed"
		}
		t.Run(name, func(t *testing.T) {
			svc, account, ticket := accountTestTicketFixture(t)
			gateway := svc.openaiGatewayService
			gateway.cfg.Gateway.OpenAICodexTicket.FailClosed = tc.closed
			model, url := "gpt-6-astra", chatgptCodexAPIURL
			switch tc.name {
			case "persisted":
				account.Extra = map[string]any{openAICodexTicketExtraKey(model): ticket}
				gateway.openaiCodexTickets.Delete(openAICodexTicketKey(account.ID, model))
			case "revoked":
				ticket.Revoked = true
			case "unverified":
				ticket.ProbeVerified = false
			case "disabled":
				gateway.cfg.Gateway.OpenAICodexTicket.Enabled = false
			case "other_model":
				model = "gpt-5.6-sol"
			case "api_key":
				account.Type = AccountTypeAPIKey
			case "other_endpoint":
				url = "https://example.invalid/v1/responses"
			case "missing":
				gateway.openaiCodexTickets.Delete(openAICodexTicketKey(account.ID, model))
				account.Extra = nil
			case "expired":
				ticket.ExpiresAt = time.Now().Add(-time.Second)
			case "websocket":
				ticket.Transport = "websocket"
			}
			calls := 0
			upstream := &accountTicketUpstream{do: func(req *http.Request, proxy string) (*http.Response, error) {
				calls++
				if tc.wantTicket {
					require.Equal(t, ticket.State, req.Header.Get(openAICodexTurnStateHeader))
					require.Equal(t, strings.Join(ticket.HarvestCookies, "; "), req.Header.Get("Cookie"))
					require.Equal(t, ticket.HarvestProxyURL, proxy)
				} else {
					require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
					require.Empty(t, req.Header.Get("Cookie"))
					require.Equal(t, "http://business.invalid:8080", proxy)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}}
			gateway.httpUpstream, svc.httpUpstream = upstream, upstream
			body := []byte(`{"model":"` + model + `","stream":true}`)
			ctx := t.Context()
			if tc.name == "acquisition" {
				ctx = WithAstraSourceAcquisition(ctx)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
			require.NoError(t, err)
			resp, err := svc.doOpenAIAccountTestUpstream(req, "http://business.invalid:8080", account, true, body)
			if tc.wantError {
				require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
				require.Nil(t, resp)
				require.Zero(t, calls, "fail-closed must not send or harvest")
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.NoError(t, resp.Body.Close())
			}
			require.False(t, gateway.codexTicketChatHeld(account.ID))
		})
	}
}

func TestAccountTestStoredTicketResponseLifecycle(t *testing.T) {
	for _, outcome := range []string{"complete", "reissued", "strict", "transport_error", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			svc, account, ticket := accountTestTicketFixture(t)
			gateway := svc.openaiGatewayService
			if outcome == "strict" {
				gateway.settingService = NewSettingService(&codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{SettingKeyOpenAICodexTicketStrict: "true"}}}, gateway.cfg)
			}
			responseBody := &codexTicketHeaderOnlyBody{}
			calls := 0
			upstream := &accountTicketUpstream{do: func(req *http.Request, proxy string) (*http.Response, error) {
				calls++
				require.True(t, gateway.codexTicketChatHeld(account.ID))
				require.Equal(t, ticket.HarvestProxyURL, proxy)
				if outcome == "transport_error" {
					return nil, io.ErrUnexpectedEOF
				}
				if outcome == "cancelled" {
					return nil, context.Canceled
				}
				h := http.Header{}
				if outcome == "reissued" {
					h.Set(openAICodexTurnStateHeader, mint780State(time.Now().Add(-time.Second)))
				}
				if outcome == "strict" {
					h.Set(openAICodexTurnStateHeader, "malformed")
				}
				return &http.Response{StatusCode: 200, Header: h, Body: responseBody}, nil
			}}
			svc.httpUpstream, gateway.httpUpstream = upstream, upstream
			// A bound ticket bypasses plugin routing just as production does; a plugin
			// must not replace one half of the bundle after admission.
			manager := &PluginManager{}
			manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "unexpected plugin routing"})
			svc.pluginManager, gateway.pluginManager = manager, manager
			body := []byte(`{"model":"gpt-6-astra","stream":true}`)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, chatgptCodexAPIURL, strings.NewReader(string(body)))
			require.NoError(t, err)
			resp, err := svc.doOpenAIAccountTestUpstream(req, "", account, true, body)
			require.Equal(t, 1, calls, "never replay a sent test")
			switch outcome {
			case "transport_error":
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				require.Nil(t, resp)
			case "cancelled":
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, resp)
			case "strict":
				require.ErrorIs(t, err, ErrCodexTicketResponseRejected)
				require.Nil(t, resp)
				require.Equal(t, 1, responseBody.closes)
			default:
				require.NoError(t, err)
				require.True(t, gateway.codexTicketChatHeld(account.ID), "guard lives until stream close")
				require.NoError(t, resp.Body.Close())
				require.NoError(t, resp.Body.Close())
			}
			require.Zero(t, responseBody.reads, "ticket observation must not consume the stream")
			require.False(t, gateway.codexTicketChatHeld(account.ID))
			if outcome == "reissued" || outcome == "strict" {
				require.True(t, gateway.lookupOpenAICodexTicket(account, "gpt-6-astra").Revoked)
			}
		})
	}
}
