package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func runtimeJSONLog(t *testing.T, identity RuntimeIdentity) []map[string]any {
	t.Helper()
	dir, err := os.MkdirTemp("", "runtime-log-fixture-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	file := filepath.Join(dir, "runtime.log")
	opts := InitOptions{Runtime: identity, Format: "json", Level: "debug", Output: OutputOptions{ToFile: true, FilePath: file}}
	base, _, err := buildLogger(opts.normalized())
	require.NoError(t, err)
	base.Info("fixture-direct")
	slog.New(newSlogZapHandler(base)).Info("fixture-slog")
	_, err = newStdLogBridge(base).Write([]byte("fixture-stdlog"))
	require.NoError(t, err)
	require.NoError(t, base.Sync())
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	result := make([]map[string]any, 0, 3)
	for decoder.More() {
		var event map[string]any
		require.NoError(t, decoder.Decode(&event))
		result = append(result, event)
	}
	return result
}

func TestRuntimeLogIdentityFieldsAndBootStability(t *testing.T) {
	opts := OptionsFromRuntimeConfig(config.LogConfig{}, config.RuntimeConfig{NodeID: "node-a", Role: "gateway"})
	require.Equal(t, RuntimeIdentity{NodeID: "node-a", Role: "gateway"}, opts.Runtime)
	first := runtimeJSONLog(t, opts.Runtime)
	second := runtimeJSONLog(t, opts.Runtime)
	require.Len(t, first, 3)
	require.Len(t, second, 3)
	for _, event := range append(first, second...) {
		require.Equal(t, "node-a", event["runtime_node_id"])
		require.Equal(t, "gateway", event["runtime_role"])
		require.Equal(t, processLogBootID, event["runtime_boot_id"])
	}
	_, err := uuid.Parse(processLogBootID)
	require.NoError(t, err)
	for _, event := range runtimeJSONLog(t, RuntimeIdentity{}) {
		require.NotContains(t, event, "runtime_node_id")
		require.NotContains(t, event, "runtime_boot_id")
	}
	for _, event := range runtimeJSONLog(t, RuntimeIdentity{NodeID: "node-a"}) {
		require.Equal(t, "full", event["runtime_role"])
	}
	for _, invalid := range []RuntimeIdentity{{NodeID: "private:secret"}, {NodeID: "node-a", Role: "secret-role"}} {
		_, _, err := buildLogger(InitOptions{Runtime: invalid})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private")
		require.NotContains(t, err.Error(), "secret-role")
	}
}

type runtimeIdentitySink struct {
	event *LogEvent
}

func (s *runtimeIdentitySink) WriteLogEvent(event *LogEvent) { s.event = event }

func TestDirectSinkUsesTrustedRuntimeIdentity(t *testing.T) {
	previousSink := loadSink()
	mu.Lock()
	previous := initOptions.Runtime
	initOptions.Runtime = RuntimeIdentity{NodeID: "node-a", Role: "gateway"}
	mu.Unlock()
	t.Cleanup(func() {
		SetSink(previousSink)
		mu.Lock()
		initOptions.Runtime = previous
		mu.Unlock()
	})
	sink := &runtimeIdentitySink{}
	SetSink(sink)
	input := map[string]any{"runtime_node_id": "fixture-untrusted", "request_id": "fixture-request"}
	WriteSinkEvent("info", "fixture", "fixture-direct-sink", input)
	require.NotNil(t, sink.event)
	require.Equal(t, "node-a", sink.event.Fields["runtime_node_id"])
	require.Equal(t, processLogBootID, sink.event.Fields["runtime_boot_id"])
	require.Equal(t, "gateway", sink.event.Fields["runtime_role"])
	require.Equal(t, "fixture-untrusted", input["runtime_node_id"], "caller fields must not be mutated")
}
