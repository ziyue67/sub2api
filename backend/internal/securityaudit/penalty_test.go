package securityaudit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type penaltyAuditScopeEngine struct{ Request Request }

func (e *penaltyAuditScopeEngine) EffectiveMode() Mode { return ModeAsync }
func (e *penaltyAuditScopeEngine) Enqueue(_ context.Context, req Request) error {
	e.Request = req
	return nil
}
func (e *penaltyAuditScopeEngine) Evaluate(context.Context, Request) (*PromptDecision, error) {
	return nil, nil
}

func TestRestrictedRoutingPreservesOriginalGroupAuditScope(t *testing.T) {
	original, target := int64(3), int64(20)
	engine := &penaltyAuditScopeEngine{}
	decision := NewCoordinator(nil, engine).Check(context.Background(), Request{UserID: 7, GroupID: &target, GroupName: "restricted", PenaltyContext: PenaltyRequestContext{SessionKey: "session", OriginalGroupID: &original, OriginalGroupName: "normal"}})
	require.True(t, decision.AllowNextStage)
	require.Equal(t, &original, engine.Request.GroupID)
	require.Equal(t, "normal", engine.Request.GroupName)
}
