//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestObserverSetupRejectsExclusiveGroupsInSimpleMode(t *testing.T) {
	svc := &adminServiceImpl{cfg: &config.Config{RunMode: config.RunModeSimple}}
	_, tx, err := svc.beginObserverSetup(context.Background(), 7, &UpdateUserInput{
		Role: RoleObserver, ObserverSetup: &ObserverSetupOptions{CreateDedicatedGroup: true},
	})
	require.Equal(t, "SIMPLE_MODE_OPERATION_UNSUPPORTED", infraerrors.Reason(err))
	require.Nil(t, tx)
}
