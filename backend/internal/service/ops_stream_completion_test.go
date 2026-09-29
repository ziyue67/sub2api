package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuccessfulStreamTerminalValidation(t *testing.T) {
	large := strings.Repeat("x", 64*1024)
	for _, tc := range []struct {
		name, data string
		want       bool
	}{
		{name: "large completed body", data: `{"output":"` + large + `","type":"response.completed","response":{"status":"completed"}}`, want: true},
		{name: "failed status after large body", data: `{"output":"` + large + `","type":"response.completed","response":{"status":"failed"}}`},
		{name: "incomplete response", data: `{"type":"response.incomplete","response":{"status":"incomplete"}}`},
		{name: "truncated JSON", data: `{"type":"response.completed","response":{"status":"completed","output":"` + large},
		{name: "completion mentioned in delta", data: `{"type":"response.output_text.delta","delta":"response.completed"}`},
		{name: "chat done", data: "[DONE]", want: true},
		{name: "anthropic stop", data: `{"type":"message_stop"}`, want: true},
		{name: "gemini stop", data: `{"candidates":[{"finishReason":"STOP"}]}`, want: true},
		{name: "gemini safety", data: `{"candidates":[{"finishReason":"SAFETY"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, IsSuccessfulStreamTerminal([]byte(tc.data)))
		})
	}
}
