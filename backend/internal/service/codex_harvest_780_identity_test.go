package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

// Do not parallelize: the canonical identity resolver is process-wide.
func TestCodex780ProbeUsesCanonicalClientIdentity(t *testing.T) {
	wasEnabled := codexIdentityEnforcement.Load()
	t.Cleanup(func() {
		SetCodexCanonicalUserAgentResolver(nil)
		SetCodexIdentityEnforcementEnabled(wasEnabled)
	})
	canonicalUA := ""
	SetCodexCanonicalUserAgentResolver(func() string { return canonicalUA })
	account := ticketTestAccount(1)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: config.OpenAICodexTicketConfig{TargetLength: 780}}}}
	state := mint780State(time.Now())
	pair := mint780Pair(time.Now().Add(time.Hour), "unified-88")

	for _, tc := range []struct {
		name, canonical, model, wantUA, wantOriginator, wantVersion string
		disableEnforcement                                          bool
	}{
		{
			name: "current canonical identity", model: "gpt-6-astra",
			canonical: "codex_cli_rs/0.200.1" + codexCLIUserAgentSuffix,
			wantUA:    "codex_cli_rs/0.200.1" + codexCLIUserAgentSuffix, wantOriginator: "codex_cli_rs", wantVersion: "0.200.1",
		},
		{
			name: "same service follows updated canonical identity", model: "gpt-6-astra",
			canonical: "codex-tui/0.201.2 (Mac OS X 14.0; arm64) iTerm",
			wantUA:    "codex_cli_rs/0.201.2 (Mac OS X 14.0; arm64) iTerm", wantOriginator: "codex_cli_rs", wantVersion: "0.201.2",
		},
		{
			name: "Astra minimum client version", model: "gpt-6-astra",
			canonical: "codex-tui/0.145.0" + codexCLIUserAgentSuffix,
			wantUA:    "codex_cli_rs/" + openAICodexAstraMinVersion + codexCLIUserAgentSuffix, wantOriginator: "codex_cli_rs", wantVersion: openAICodexAstraMinVersion,
		},
		{
			name: "non Astra keeps configured version", model: "gpt-5.6-sol",
			canonical: "codex-tui/0.145.0" + codexCLIUserAgentSuffix,
			wantUA:    "codex_cli_rs/0.145.0" + codexCLIUserAgentSuffix, wantOriginator: "codex_cli_rs", wantVersion: "0.145.0",
		},
		{
			name: "invalid canonical identity falls back safely", model: "gpt-5.6-sol", canonical: "invalid-client",
			wantUA: codexCLIUserAgent, wantOriginator: "codex_cli_rs", wantVersion: codexCLIVersion,
		},
		{
			name: "enforcement disabled still supplies paired identity", model: "gpt-6-astra", disableEnforcement: true,
			canonical: "codex-tui/0.200.1" + codexCLIUserAgentSuffix,
			wantUA:    "codex_cli_rs/0.200.1" + codexCLIUserAgentSuffix, wantOriginator: "codex_cli_rs", wantVersion: "0.200.1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonicalUA = tc.canonical
			SetCodexIdentityEnforcementEnabled(!tc.disableEnforcement)
			calls := 0
			svc.httpUpstream = &harvestProxyUpstream{do: func(req *http.Request, proxy string) (*http.Response, error) {
				calls++
				require.Equal(t, tc.wantUA, req.Header.Get("User-Agent"))
				require.Equal(t, tc.wantOriginator, req.Header.Get("Originator"))
				// Keep legacy TUI inputs above: the real harvest adapter must
				// normalize both headers, never send a mixed CLI/TUI pair.
				require.NotContains(t, req.Header.Get("User-Agent"), "codex-tui")
				pairedOriginator, pairedUA, paired := openai.PairCodexClientIdentity(req.Header.Get("User-Agent"))
				require.True(t, paired)
				require.Equal(t, req.Header.Get("Originator"), pairedOriginator)
				require.Equal(t, req.Header.Get("User-Agent"), pairedUA)
				require.Equal(t, tc.wantVersion, req.Header.Get("Version"))
				require.Equal(t, "responses=experimental", req.Header.Get("OpenAI-Beta"))
				require.Equal(t, "session", req.Header.Get("session_id"))
				require.Equal(t, "session", req.Header.Get("session-id"))
				require.Equal(t, "socks5://127.0.0.1:1080", proxy)
				require.Equal(t, HTTPUpstreamProfileOpenAIHarvest, HTTPUpstreamProfileFromContext(req.Context()))
				require.True(t, req.Close)
				if calls == 1 {
					require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
					require.Empty(t, req.Header.Get("Cookie"))
				} else {
					require.Equal(t, state, req.Header.Get(openAICodexTurnStateHeader))
					require.Equal(t, strings.Join(pair, "; "), req.Header.Get("Cookie"))
				}
				h := http.Header{"Set-Cookie": pair}
				h.Set(openAICodexTurnStateHeader, state)
				return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(harvestProbeCompleteFixture(tc.model)))}, nil
			}}
			out := svc.requestCodex780Probe(context.Background(), account, "token", tc.model, "socks5://127.0.0.1:1080", func() bool { return true }, "session")
			require.NoError(t, out.Err)
			require.True(t, out.ProbeVerified)
			require.Equal(t, 2, calls)
		})
	}
}
