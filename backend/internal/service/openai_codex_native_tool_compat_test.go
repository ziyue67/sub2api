package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStripOpenAICodexUnsupportedWebSearchFields(t *testing.T) {
	body := []byte(`{
		"model":"gpt-6-astra",
		"tools":[
			{"type":"function","name":"exec_command","parameters":{"type":"object"}},
			{"type":"web_search","external_web_access":true,"search_context_size":"high"},
			{"type":"web_search_preview","external_web_access":false},
			{"type":"function","name":"keep_me","external_web_access":true,"parameters":{"type":"object"}}
		],
		"input":"hello"
	}`)

	got, changed, err := stripOpenAICodexUnsupportedWebSearchFields(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(got, "tools.1.external_web_access").Exists())
	require.Equal(t, "high", gjson.GetBytes(got, "tools.1.search_context_size").String())
	require.False(t, gjson.GetBytes(got, "tools.2.external_web_access").Exists())
	require.True(t, gjson.GetBytes(got, "tools.3.external_web_access").Bool())
	require.Equal(t, "hello", gjson.GetBytes(got, "input").String())
}

func TestStripOpenAICodexUnsupportedWebSearchFieldsNoop(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"model":"gpt-6-astra","tools":[{"type":"function","name":"exec_command"}]}`),
		[]byte(`{"model":"gpt-6-astra","tools":[{"type":"web_search","search_context_size":"high"}]}`),
		[]byte(`{"model":"gpt-6-astra"}`),
		[]byte(`not-json`),
	} {
		got, changed, err := stripOpenAICodexUnsupportedWebSearchFields(body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, got)
	}
}
