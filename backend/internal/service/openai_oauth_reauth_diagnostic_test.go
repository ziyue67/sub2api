package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionStudioDiagnosticOnlyContainsObservedClientPhase(t *testing.T) {
	for _, fields := range []string{
		"stage=http_request reason=http_rejected elapsed_ms=120 http_status=403",
		"stage=event_stream reason=duplicate_field elapsed_ms=120 http_status=0",
		"stage=terminal_result reason=provider_failed elapsed_ms=120 http_status=0",
		"stage=credentials_validation reason=credentials_incomplete elapsed_ms=120 http_status=0",
		"stage=credentials_submit reason=credentials_not_accepted elapsed_ms=120 http_status=0",
	} {
		raw := "session_studio_failed " + fields
		require.Equal(t, raw, sanitizeReauthError(raw))
		task := safeReauthTask(&OpenAIOAuthReauthTaskRecord{Error: raw, Status: "failed", Stage: "failed"})
		require.NotNil(t, task.Diagnostic)
		require.Equal(t, OpenAIOAuthReauthEngineSessionStudio, task.Diagnostic.Engine)
		require.Equal(t, "failed", task.Status)
		require.Equal(t, "failed", task.Stage)
		require.Equal(t, 120, task.Diagnostic.ElapsedMS)
		require.Nil(t, task.Diagnostic.ExitCode)
		encoded, err := json.Marshal(task)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "exit_code")
	}
	valid := "session_studio_failed stage=terminal_result reason=provider_failed elapsed_ms=120 http_status=0"
	for _, raw := range []string{
		strings.Replace(valid, "terminal_result", "token_exchange", 1), // Remote internal phase is unknown.
		strings.Replace(valid, "provider_failed", "synthetic-private", 1),
		strings.Replace(valid, "120", "-1", 1), strings.Replace(valid, "120", "3600001", 1),
		strings.Replace(valid, "http_status=0", "http_status=403", 1),
		strings.Replace(valid, "provider_failed", "http_rejected", 1),
		"session_studio_failed stage=http_request reason=http_rejected elapsed_ms=0 http_status=600",
		"session_studio_failed stage=http_request reason=http_rejected elapsed_ms=0 http_status=0",
		valid + " synthetic-private", valid + "\nsynthetic-private", valid + strings.Repeat("x", 5000),
	} {
		require.Equal(t, "Session Studio failed; invalid diagnostic omitted", sanitizeReauthError(raw))
		require.Nil(t, safeReauthTask(&OpenAIOAuthReauthTaskRecord{Error: raw}).Diagnostic)
	}
}

func TestSessionStudioDiagnosticFailureUsesExistingTaskOwnership(t *testing.T) {
	svc, _, repo, updater, _, _ := newReauthTestService("acct-1")
	ctx := context.Background()
	_, err := svc.SaveCredentialConfig(ctx, 42, OpenAIOAuthReauthConfigInput{
		LoginEmail: "user@example.test", CredentialMode: OpenAIOAuthReauthModePasswordTOTP,
		Engine: OpenAIOAuthReauthEngineSessionStudio, Password: "synthetic-password",
	})
	require.NoError(t, err)
	_, err = svc.CreateTask(ctx, 42)
	require.NoError(t, err)
	claim, err := svc.ClaimTaskWithEngines(ctx, "worker-a", []string{OpenAIOAuthReauthEngineSessionStudio})
	require.NoError(t, err)
	require.NotNil(t, claim)
	raw := "session_studio_failed stage=terminal_result reason=provider_failed elapsed_ms=120 http_status=0"
	require.Error(t, svc.FailTask(ctx, claim.TaskID, "worker-b", raw))
	require.Equal(t, OpenAIOAuthReauthStatusRunning, repo.task.Status)
	require.NoError(t, svc.FailTask(ctx, claim.TaskID, "worker-a", raw))
	require.Equal(t, OpenAIOAuthReauthStatusFailed, repo.task.Status)
	require.Equal(t, raw, repo.task.Error)
	require.False(t, updater.applied)
	require.NotNil(t, safeReauthTask(repo.task).Diagnostic)
}

func TestLocalProtocolDiagnosticIsBoundedAndPrivate(t *testing.T) {
	valid := "local_protocol_failed stage=callback_validation reason=callback_state_mismatch elapsed_ms=120 exit=1"
	require.Equal(t, valid, sanitizeReauthError("WorkerError: "+valid))
	task := safeReauthTask(&OpenAIOAuthReauthTaskRecord{Error: valid, Status: "failed", Stage: "failed"})
	require.Equal(t, "failed", task.Stage)
	require.Equal(t, "callback_validation", task.Diagnostic.Stage)
	require.Equal(t, 120, task.Diagnostic.ElapsedMS)
	for _, raw := range []string{
		strings.Replace(valid, "callback_validation", "synthetic-private", 1),
		strings.Replace(valid, "callback_state_mismatch", "synthetic-private", 1),
		strings.Replace(valid, "120", "3600001", 1),
		strings.Replace(valid, "120", "-1", 1),
		strings.Replace(valid, "exit=1", "exit=256", 1),
		valid + " synthetic-private", valid + "\nsynthetic-private", valid + strings.Repeat("x", 5000),
	} {
		t.Run(raw[:min(len(raw), 70)], func(t *testing.T) {
			require.Equal(t, "local protocol failed; invalid diagnostic omitted", sanitizeReauthError(raw))
			require.Nil(t, safeReauthTask(&OpenAIOAuthReauthTaskRecord{Error: raw}).Diagnostic)
		})
	}
	// Existing workers and task errors do not require the optional protocol.
	require.Equal(t, "password/TOTP protocol failed (exit 1)", sanitizeReauthError("password/TOTP protocol failed (exit 1)"))
}
