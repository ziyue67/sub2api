//go:build integration

package authoutbox_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/stretchr/testify/require"
)

type claimProcessInput struct {
	DSN, Owner string
}

type claimProcessReply struct {
	ID    int64
	Stage int
	Error string
}

// Re-exec helper only: the fixture DSN arrives through anonymous stdin, never
// argv, environment or logs. This child claims through the real repository,
// then blocks before any Redis side effect until the parent terminates it.
func TestAuthOutboxClaimProcess(t *testing.T) {
	if os.Getenv("SUB2API_TEST_AUTH_CLAIM_CHILD") != "1" {
		return
	}
	out := os.NewFile(3, "claim-reply")
	require.NotNil(t, out)
	defer func() { _ = out.Close() }()
	encoder := json.NewEncoder(out)
	fail := func() {
		_ = encoder.Encode(claimProcessReply{Error: "isolated claim fixture failed"})
		t.FailNow() // Do not print a DSN or a driver error containing fixture addresses.
	}
	decoder := json.NewDecoder(os.Stdin)
	var input claimProcessInput
	if decoder.Decode(&input) != nil {
		fail()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", input.DSN)
	if err != nil {
		fail()
	}
	defer func() { _ = db.Close() }()
	events, err := repository.NewAuthCacheInvalidationOutboxRepository(db).Claim(ctx, input.Owner, 1, 30*time.Second)
	if err != nil || len(events) != 1 {
		fail()
	}
	if encoder.Encode(claimProcessReply{ID: events[0].ID, Stage: events[0].Stage}) != nil {
		fail()
	}
	var release struct{}
	_ = decoder.Decode(&release) // Parent keeps stdin open and sends no release.
}

type outboxClaimProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	reply   claimProcessReply
	waited  bool
}

func startOutboxClaimProcess(t *testing.T, dsn, owner string) *outboxClaimProcess {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	readReply, writeReply, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = readReply.Close() })
	t.Cleanup(func() { _ = writeReply.Close() })
	//nolint:gosec // G204: re-exec only this test binary with a fixed helper name; fixture DSN uses stdin.
	command := exec.Command(executable, "-test.run=^TestAuthOutboxClaimProcess$", "-test.timeout=90s")
	command.Env = []string{"SUB2API_TEST_AUTH_CLAIM_CHILD=1", "GOMAXPROCS=1"}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.ExtraFiles = []*os.File{writeReply}
	command.WaitDelay = 5 * time.Second
	input, err := command.StdinPipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = input.Close() })
	child := &outboxClaimProcess{command: command, input: input}
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		if !child.waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		_ = input.Close()
	})
	require.NotEqual(t, os.Getpid(), command.Process.Pid)
	require.NoError(t, writeReply.Close())
	require.NoError(t, json.NewEncoder(input).Encode(claimProcessInput{DSN: dsn, Owner: owner}))
	require.NoError(t, readReply.SetReadDeadline(time.Now().Add(10*time.Second)))
	require.NoError(t, json.NewDecoder(readReply).Decode(&child.reply))
	require.Empty(t, child.reply.Error)
	return child
}

func (child *outboxClaimProcess) crash(t *testing.T) {
	t.Helper()
	require.NoError(t, child.command.Process.Kill())
	err := child.command.Wait()
	child.waited = true
	require.Error(t, err, "the claiming process must actually terminate abnormally")
	// Wait closes StdinPipe itself; a second Close reports os.ErrClosed.
}

func TestDatabaseOutboxReclaimsCrashedClaimAfterOriginalLease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated child fixture uses inherited reply descriptors")
	}
	testDatabaseOutboxHTTPRevocation(t, outboxHTTPScenario{safetyPass: true, crashClaim: true})
}
