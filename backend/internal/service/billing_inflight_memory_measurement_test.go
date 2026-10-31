//go:build unit

package service

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	inflightMemoryChildEnv   = "SUB2API_TEST_INFLIGHT_MEMORY_CHILD"
	inflightMemoryControlEnv = "SUB2API_TEST_INFLIGHT_MEMORY_CONTROL"
	inflightMemoryTestName   = "TestInflightEstimate_AccountMappingNoDBAndBoundedMemory"
)

func inflightMemoryControl() string { return os.Getenv(inflightMemoryControlEnv) }

func inflightMemoryCommand(t *testing.T, control string) ([]byte, error) {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^"+inflightMemoryTestName+"$", "-test.v", "-test.count=1", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), inflightMemoryChildEnv+"=1", inflightMemoryControlEnv+"="+control)
	return cmd.CombinedOutput()
}

func runInflightMemoryInSubprocess(t *testing.T) bool {
	t.Helper()
	if os.Getenv(inflightMemoryChildEnv) == "1" {
		return false
	}
	output, err := inflightMemoryCommand(t, "")
	t.Logf("isolated estimator workload:\n%s", output)
	require.NoError(t, err, "isolated estimator must keep the original 8 MiB bound")
	require.Contains(t, string(output), "inflight_heap_probe")
	return true
}

func inflightMemoryUnrelatedHeapControl() []byte {
	if inflightMemoryControl() != "unrelated_heap" {
		return nil
	}
	ready := make(chan []byte)
	go func() {
		// This allocation is outside EstimateInflightReservation, but the old
		// process-wide measurement charged it to the estimator all the same.
		heap := make([]byte, 16<<20)
		heap[0] = 1
		ready <- heap
	}()
	return <-ready
}

func TestInflightEstimate_MemoryMeasurementControls(t *testing.T) {
	for _, control := range []string{"unrelated_heap", "retained_model_cache"} {
		t.Run(control, func(t *testing.T) {
			output, err := inflightMemoryCommand(t, control)
			t.Logf("expected failing memory control %s:\n%s", control, output)
			require.Error(t, err, "the unchanged 8 MiB guard must reject the injected retention")
			require.Contains(t, string(output), "--- FAIL: "+inflightMemoryTestName)
			require.Contains(t, string(output), "no per-model cache growth")
			require.Contains(t, string(output), "control=\""+control+"\"")
		})
	}
}
