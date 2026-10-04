//go:build unit

package openai_ws_v2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebSearchCallCounterDeduplicatesAndResetsPerTurn(t *testing.T) {
	var counter webSearchCallCounter
	counter.Observe([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws_1","call_id":"call_1","status":"completed"}}`), "response.output_item.done")
	counter.Observe([]byte(`{"type":"response.output_item.done","output_index":1,"item":{"type":"web_search_call","id":"ws_in_progress","status":"in_progress"}}`), "response.output_item.done")
	counter.Observe([]byte(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"web_search_call","id":"ws_1","call_id":"call_1"}]}}`), "response.completed")
	require.Equal(t, 1, counter.Take())
	require.Zero(t, counter.Take())
}

func TestWebSearchCallCounterFailedTerminalKeepsCompletedItem(t *testing.T) {
	var counter webSearchCallCounter
	counter.Observe([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws_1","status":"completed"}}`), "response.output_item.done")
	counter.Observe([]byte(`{"type":"response.incomplete","response":{"status":"incomplete"}}`), "response.incomplete")
	require.Equal(t, 1, counter.Take())
}
