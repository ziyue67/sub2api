package logger

import (
	"errors"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Diagnostic fields only: never use them as authentication or WS owner tokens.
type RuntimeIdentity struct {
	NodeID string
	Role   string
}

var processLogBootID = uuid.NewString()

func (r RuntimeIdentity) validate() error {
	if r.NodeID == "" {
		return nil
	}
	if len(r.NodeID) > 64 || (r.Role != "" && r.Role != "full" && r.Role != "gateway") {
		return errors.New("invalid runtime log identity")
	}
	for _, ch := range r.NodeID {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' {
			return errors.New("invalid runtime log identity")
		}
	}
	return nil
}

func (r RuntimeIdentity) fields() []zap.Field {
	if r.NodeID == "" {
		return nil
	}
	role := r.Role
	if role == "" {
		role = "full"
	}
	return []zap.Field{zap.String("runtime_node_id", r.NodeID), zap.String("runtime_boot_id", processLogBootID), zap.String("runtime_role", role)}
}

func runtimeLogIdentity() RuntimeIdentity {
	mu.RLock()
	defer mu.RUnlock()
	return initOptions.Runtime
}
