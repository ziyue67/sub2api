package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The baseline silently dropped this terminal, zero-image attempt. It must
// remain inspectable even though generation and billing are separate outcomes.
func TestImageHistoryRetainsZeroImageAttempt(t *testing.T) {
	s, repo := archiveTestService(t)
	c, _ := archiveTestContext()
	capture := archiveTestCapture(s, c)
	require.NotNil(t, capture)
	capture.Finish(c, &OpenAIForwardResult{ImageCount: 0})
	select {
	case job := <-s.queue:
		s.process(job)
	default:
		t.Fatal("zero-image terminal attempt disappeared from history")
	}
	require.Len(t, repo.records, 1)
	require.Empty(t, repo.records[capture.record.ID].Files)
}
