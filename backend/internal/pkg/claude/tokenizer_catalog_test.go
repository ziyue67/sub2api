package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsesLegacyTokenizer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  bool
	}{
		{model: "claude-opus-4-6", want: true},
		{model: "claude-opus-4-5-20251101", want: true},
		{model: "claude-sonnet-4-6", want: true},
		{model: "claude-sonnet-4-5-20250929", want: true},
		{model: "claude-haiku-4-5-20251001", want: true},
		{model: "claude-opus-4-1-20250805", want: true},
		{model: "claude-opus-4-20250514", want: true},
		{model: "claude-sonnet-4-20250514", want: true},
		{model: "claude-3-7-sonnet-20250219", want: true},
		{model: "claude-3-5-haiku-20241022", want: true},
		{model: "us.anthropic.claude-sonnet-4-6", want: true},
		{model: "anthropic/claude-sonnet-4.5", want: true},
		{model: "claude-opus-4.6-20260205", want: true},
		{model: "claude-haiku-4.5", want: true},
		{model: "claude-opus-4-7", want: false},
		{model: "claude-opus-4.7", want: false},
		{model: "claude-opus-4-8", want: false},
		{model: "claude-opus-5-5", want: false},
		{model: "anthropic/claude-opus-5.5", want: false},
		{model: "claude-sonnet-5", want: false},
		{model: "claude-haiku-5-5", want: false},
		{model: "claude-fable-5-1", want: false},
		{model: "claude-unknown-next", want: false},
		{model: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, UsesLegacyTokenizer(tt.model))
		})
	}
}
