package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQualityObservationNeverReadsOrMutatesAccounts(t *testing.T) {
	for _, outcome := range []string{"passed", "failed", "inconclusive"} {
		t.Run(outcome, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			plan := &service.ScheduledTestPlan{ID: 7, AccountID: 8, UpdatedAt: time.Now(), PelicanConfig: &service.PelicanTestConfig{
				Quality: &service.QualityPolicy{Action: service.QualityActionObserveOnly, AutoRestore: true}}}
			until := time.Now().Add(time.Minute)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT enabled AND updated_at").WithArgs(plan.ID, plan.UpdatedAt, until).WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(true))
			mock.ExpectExec("DELETE FROM account_quality_states WHERE plan_id").WithArgs(plan.ID).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			got, err := NewScheduledTestPlanRepository(db).ApplyQualityOutcome(context.Background(), plan, until, outcome)
			require.NoError(t, err)
			require.Equal(t, "observed", got)
			require.NoError(t, mock.ExpectationsWereMet(), "no account, group, restore or outbox queries are permitted")
		})
	}
}
