package main

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRegionalCleanupJoinsFundsBeforeDependencies(t *testing.T) {
	finished := false
	require.NoError(t, stopRegionalFunds(context.Background(), &service.RegionalMetering{Stop: func() { finished = true }}))
	require.True(t, finished)
	release := make(chan struct{})
	exited := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := stopRegionalFunds(ctx, &service.RegionalMetering{Stop: func() { defer close(exited); <-release }})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	<-exited
}
