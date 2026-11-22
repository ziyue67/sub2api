package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSConflictingRequestFieldsErrorPreservesRequestScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(error) error
	}{
		{name: "direct", wrap: func(err error) error { return err }},
		{name: "wrapped", wrap: func(err error) error { return fmt.Errorf("ingress: %w", err) }},
		{name: "passthrough relay", wrap: func(err error) error {
			return wrapOpenAIWSIngressTurnError("read_client", err, true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.wrap(NewOpenAIWSConflictingRequestFieldsError("reasoning.effort"))
			require.ErrorIs(t, err, ErrOpenAIWSConflictingRequestFields)
			var closeErr *OpenAIWSClientCloseError
			require.ErrorAs(t, err, &closeErr)
			require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
			require.Equal(t, `websocket request payload declares field "reasoning.effort" more than once`, closeErr.Reason())
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
		})
	}
}

func TestOpenAIWSConflictingRequestFieldsErrorBoundsClientField(t *testing.T) {
	err := NewOpenAIWSConflictingRequestFieldsError(strings.Repeat("x", 100) + "\n")
	require.Equal(t, fmt.Sprintf("websocket request payload declares field %q more than once", strings.Repeat("x", 64)), err.Reason())
}
