package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type astraTargetProbeStub struct {
	*astraSetupUpstream
	calls   int
	err     error
	request *http.Request
}

func (p *astraTargetProbeStub) VerifyAstraGatewayTarget(_ context.Context, req *http.Request, _ string, _ int64, _ int) error {
	p.calls++
	p.request = req
	return p.err
}
func TestAstraGatewayVerifyAlwaysUsesStateProbe(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{8}, TargetAccountIDs: []int64{7}}
	a := stateProbeAccount()
	a.Status = StatusActive
	provider := &astraTargetProbeStub{astraSetupUpstream: &astraSetupUpstream{}}
	svc := &AccountTestService{cfg: cfg, accountRepo: &stateProbeAccountRepo{account: a}, httpUpstream: provider, openaiGatewayService: &OpenAIGatewayService{}}
	for _, kind := range []string{"", "state_probe", "candy"} {
		result, err := svc.TestAstraGateway(t.Context(), "verify", 7, 1, kind)
		require.NoError(t, err)
		require.True(t, result.Success)
		require.Equal(t, "state_probe", result.TestKind)
		require.Empty(t, result.Answer)
		require.Empty(t, result.Expected)
		require.Equal(t, "Bearer tok-7", provider.request.Header.Get("Authorization"))
		require.Equal(t, "acct-7", provider.request.Header.Get("ChatGPT-Account-ID"))
		require.Nil(t, provider.request.Body, "no third question after route validation")
	}
	require.Equal(t, 3, provider.calls)
	provider.err = errors.New("target_probe_degraded")
	result, err := svc.TestAstraGateway(t.Context(), "verify", 7, 1)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, "target_probe_degraded", result.Reason)
}
