//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthInitialQualityAtomicCreate(t *testing.T) {
	for _, withGroups := range []bool{false, true} {
		for _, invalid := range []bool{false, true} {
			t.Run(fmt.Sprintf("groups=%v/invalid=%v", withGroups, invalid), func(t *testing.T) {
				ctx := t.Context()
				repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
				now := time.Now()
				name := fmt.Sprintf("initial-quality-%d", now.UnixNano())
				a := &service.Account{Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Concurrency: 3, Schedulable: true,
					InitialQualityPlan: &service.ScheduledTestPlan{ModelID: "gpt-5.4", CronExpression: "*/5 * * * *", Enabled: true, MaxResults: 100, NextRunAt: &now, PelicanConfig: &service.PelicanTestConfig{QuestionKind: "state_probe", ParallelCount: 1, ReasoningEffort: "high", Quality: &service.QualityPolicy{Action: service.QualityActionObserveOnly}}}}
				// Database varchar constraint fails only when inserting the rule, after account creation.
				if invalid {
					a.InitialQualityPlan.ModelID = strings.Repeat("x", 1000)
				}
				t.Cleanup(func() {
					ctx := context.Background()
					_, err := integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
					require.NoError(t, err)
					_, err = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE name=$1", name)
					require.NoError(t, err)
				})
				var err error
				if withGroups {
					err = repo.CreateWithAccountGroups(ctx, a, nil)
				} else {
					err = repo.Create(ctx, a)
				}
				if invalid {
					require.Error(t, err)
					var count int
					require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE name=$1", name).Scan(&count))
					require.Zero(t, count)
					return
				}
				require.NoError(t, err)
				plans, err := NewScheduledTestPlanRepository(integrationDB).ListByAccountID(ctx, a.ID)
				require.NoError(t, err)
				require.Len(t, plans, 1)
				require.True(t, plans[0].Enabled)
				require.Equal(t, service.QualityActionObserveOnly, plans[0].PelicanConfig.Quality.Action)
				a.Name = name + "-edited"
				require.NoError(t, repo.Update(ctx, a))
				plans, err = NewScheduledTestPlanRepository(integrationDB).ListByAccountID(ctx, a.ID)
				require.NoError(t, err)
				require.Len(t, plans, 1)
				// Restore the name used by cleanup.
				_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET name=$2 WHERE id=$1", a.ID, name)
				require.NoError(t, err)
			})
		}
	}
}
