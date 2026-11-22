package service

import (
	"errors"
	"fmt"

	coderws "github.com/coder/websocket"
)

// ErrOpenAIWSConflictingRequestFields identifies a local client-payload rejection.
// Keep it recognizable through ingress wrappers so it never counts against the
// selected upstream account's health or scheduling score.
var ErrOpenAIWSConflictingRequestFields = errors.New("openai websocket request contains conflicting fields")

// NewOpenAIWSConflictingRequestFieldsError closes only the offending client
// session. A 1008 close code alone cannot identify a local rejection because
// upstream authentication and policy failures can use the same code.
func NewOpenAIWSConflictingRequestFieldsError(field string) *OpenAIWSClientCloseError {
	return NewOpenAIWSClientCloseError(
		coderws.StatusPolicyViolation,
		fmt.Sprintf("websocket request payload declares field %.64q more than once", field),
		ErrOpenAIWSConflictingRequestFields,
	)
}
