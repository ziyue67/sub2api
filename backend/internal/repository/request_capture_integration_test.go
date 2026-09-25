//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestRequestCaptureSQLLifecycle(t *testing.T) {
	ctx := context.Background()
	store := &requestcapture.SQLStore{DB: integrationDB}
	m, err := requestcapture.New(store, t.TempDir(), requestcapture.Config{Enabled: true, QuotaMiB: 1, RetentionDays: 7})
	require.NoError(t, err)
	defer m.Close()
	task, err := m.Create(ctx, requestcapture.CreateTask{TargetType: "user", TargetID: 1, DurationMinutes: 1}, "synthetic fixture")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.DeleteTask(ctx, m.InstanceID(), task.ID)) })
	s := m.Begin(requestcapture.Meta{UserID: 1, RequestID: "capture-sql-fixture"})
	require.NotNil(t, s)
	s.ClientRequest([]byte(`{"api_key":"DO-NOT-PERSIST","input":"PRIVATE-PAYLOAD"}`), "application/json", nil)
	s.Finish(403)
	require.Eventually(t, func() bool { return m.Stats().ActiveRequests == 0 }, 5*time.Second, 10*time.Millisecond)
	rows, err := m.Records(ctx, task.ID, "capture-sql-fixture", true, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].Parts, "lists must omit large metadata arrays")
	record, err := m.Record(ctx, task.ID, rows[0].ID)
	require.NoError(t, err)
	require.Len(t, record.Parts, 1)
	require.True(t, record.IsError)
	text, _, _, err := m.Preview(ctx, task.ID, record.ID, record.Parts[0].Name, 0)
	require.NoError(t, err)
	require.Contains(t, text, "PRIVATE-PAYLOAD")
	require.NotContains(t, text, "DO-NOT-PERSIST")
	var metadata string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT data::text FROM request_capture_records WHERE id=$1", record.ID).Scan(&metadata))
	require.False(t, strings.Contains(metadata, "PRIVATE-PAYLOAD"))
	require.False(t, strings.Contains(metadata, "DO-NOT-PERSIST"))
	_, err = store.Task(ctx, "another-instance", task.ID)
	require.Error(t, err)
	// Successful traffic must leave no raw SQL index, including when queried
	// without the manager's mandatory error-only policy.
	success := m.Begin(requestcapture.Meta{UserID: 1, RequestID: "successful-request"})
	require.NotNil(t, success)
	success.ClientRequest([]byte("{\"input\":\"SUCCESS_MUST_DISAPPEAR\"}"), "application/json", nil)
	success.Finish(200)
	require.Eventually(t, func() bool { return m.Stats().ActiveRequests == 0 }, 5*time.Second, time.Millisecond)
	raw, err := store.Records(ctx, task.ID, "successful-request", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, raw)
	// Old success rows and unfinalized error rows are hidden by the SQL predicate.
	now := time.Now().UTC()
	for _, pending := range []bool{false, true} {
		hidden := &requestcapture.Record{ID: uuid.NewString(), TaskID: task.ID, InstanceID: m.InstanceID(), CreatedAt: now, FinishedAt: &now, IsError: pending}
		if pending {
			hidden.FinishedAt = nil
		}
		require.NoError(t, store.SaveRecord(ctx, hidden))
		visible, err := m.Records(ctx, task.ID, "", false, 10, 0)
		require.NoError(t, err)
		require.Len(t, visible, 1)
		_, err = m.Record(ctx, task.ID, hidden.ID)
		require.ErrorIs(t, err, requestcapture.ErrNotFound)
		require.NoError(t, store.DeleteRecord(ctx, task.ID, hidden.ID))
	}
	require.NoError(t, m.Delete(ctx, task.ID))
	rows, err = store.Records(ctx, task.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, m.Stats().UsedBytes)
}
