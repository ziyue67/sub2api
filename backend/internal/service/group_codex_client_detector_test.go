package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexGroupGateIsIndependentOfUpstreamAccountAndForceCodexCLI(t *testing.T) {
	c := newCodexDetectorTestContext("OpenAI/Python", "")
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	detector := NewOpenAICodexClientRestrictionDetector(&config.Config{Gateway: config.GatewayConfig{ForceCodexCLI: true}})
	require.False(t, detector.Detect(c, account, CodexRestrictionPolicy{}, nil).Enabled)
	result := DetectCodexGroupClient(c, &Group{CodexCLIOnly: true}, CodexRestrictionPolicy{}, nil)
	require.True(t, result.Enabled)
	require.False(t, result.Matched, "upstream compatibility must not authenticate an employee")
}
