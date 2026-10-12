//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDBReservationStoreGenericScopeReserveAndRelease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	store := &dbReservationStore{db: db}
	ctx := context.Background()
	const (
		scope     = "sub:42:7"
		requestID = "request-generic"
		ttl       = 5 * time.Minute
		amount    = 0.10
		maxTotal  = 0.30
	)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs(scope).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM billing_scope_reservations").
		WithArgs(scope).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT amount::double precision").
		WithArgs(scope, requestID).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}))
	mock.ExpectQuery("SELECT COALESCE").
		WithArgs(scope).
		WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(float64(0)))
	mock.ExpectExec("INSERT INTO billing_scope_reservations").
		WithArgs(scope, requestID, amount, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 不再随每笔 DB 预留顺延共享窗口（审计 H1：顺延让窗口在持续流量下永不结束）。
	mock.ExpectCommit()

	total, accepted, err := store.tryReserve(ctx, scope, requestID, amount, maxTotal, ttl)
	require.NoError(t, err)
	require.True(t, accepted)
	require.InDelta(t, amount, total, 1e-12)

	mock.ExpectExec("DELETE FROM billing_scope_reservations").
		WithArgs(scope, requestID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.release(ctx, scope, requestID))
	require.NoError(t, mock.ExpectationsWereMet())
}
