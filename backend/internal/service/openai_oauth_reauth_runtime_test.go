package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOAuthReauthManagedWorkerNeedsNoEnvironmentToken(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
	t.Setenv("DATA_DIR", t.TempDir())
	s := &OpenAIOAuthReauthService{}
	cfg := &config.Config{}
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 4040
	s.configureWorker(cfg, BuildInfo{Version: "2.9.4"})
	defer s.stopWorker()
	require.NotNil(t, s.worker)
	require.Len(t, s.workerToken, 64)
	configured, valid := s.WorkerAuthentication(s.workerToken)
	require.True(t, configured)
	require.True(t, valid)
	_, valid = s.WorkerAuthentication(strings.Repeat("x", 64))
	require.False(t, valid)
	status := s.WorkerStatus()
	require.Equal(t, "managed", status.Mode)
	require.Equal(t, "idle", status.State)
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(raw), s.workerToken)
	require.Error(t, s.checkWorkerMode(OpenAIOAuthReauthModeEmailOTPURL))
}

func TestOpenAIOAuthReauthPreservesExternalWorker(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", strings.Repeat("a", 32))
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil, BuildInfo{Version: "2.9.4"})
	require.Nil(t, s.worker)
	configured, valid := s.WorkerAuthentication(strings.Repeat("a", 32))
	require.True(t, configured)
	require.True(t, valid)
	require.Equal(t, "external_offline", s.WorkerStatus().Reason)
}

func TestOpenAIOAuthReauthShortExplicitTokenDoesNotStartCompetingWorker(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "short")
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil, BuildInfo{Version: "2.9.4"})
	require.Nil(t, s.worker)
	configured, valid := s.WorkerAuthentication("short")
	require.False(t, configured)
	require.False(t, valid)
	require.Equal(t, "external_not_configured", s.WorkerStatus().Reason)
}
