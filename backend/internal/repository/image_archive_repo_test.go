package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestImageArchiveRepositoryFiltersAndPagination(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewImageArchiveRepository(db)
	from := time.Now().Add(-time.Hour)
	to := time.Now()
	filter := service.ImageArchiveFilter{Page: 2, PageSize: 24, UserID: 12, GroupID: 8, AccountID: 7, Model: "x' OR 1=1 --", From: &from, To: &to}
	where, args := imageArchiveWhere(filter)
	require.Equal(t, " WHERE a.expires_at > NOW() AND a.user_id = $1 AND a.account_id = $2 AND a.group_id = $3 AND a.model = $4 AND a.created_at >= $5 AND a.created_at <= $6", where)
	require.Equal(t, filter.Model, args[3])
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM image_request_archives a`+where)).WithArgs(int64(12), int64(7), int64(8), filter.Model, from, to).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	columns := []string{"id", "user_id", "api_key_id", "group_id", "account_id", "request_id", "model", "upstream_model", "endpoint", "prompt", "parameters", "files", "status", "error_codes", "byte_size", "created_at", "expires_at", "email", "key_name", "group_name", "account_name"}
	mock.ExpectQuery(regexp.QuoteMeta(imageArchiveSelect+where+` ORDER BY a.created_at DESC,a.id DESC LIMIT $7 OFFSET $8`)).WithArgs(int64(12), int64(7), int64(8), filter.Model, from, to, 24, 24).WillReturnRows(sqlmock.NewRows(columns).AddRow(uuid.NewString(), 12, 34, 8, 7, "request", "image", "upstream", "/v1/images/edits", "prompt", `{"size":"auto"}`, `[]`, "saved", "", 128, from, to, "user@example.com", "key", "group", "account"))
	items, total, err := repo.List(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	require.Equal(t, "user@example.com", items[0].UserEmail)
	require.Equal(t, int64(34), items[0].APIKeyID)
	require.Equal(t, "auto", items[0].Parameters["size"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageArchiveRepositoryWriteAndStorageLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewImageArchiveRepository(db)
	ctx := context.Background()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(719240300)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectRollback()
	release, err := repo.Lock(ctx)
	require.NoError(t, err)
	release()
	now := time.Now()
	record := &service.ImageArchiveRecord{ID: uuid.NewString(), UserID: 12, APIKeyID: 34, AccountID: 7, Model: "image", Endpoint: "/v1/images/generations", Prompt: "prompt", Parameters: map[string]any{}, Files: []service.ImageArchiveFile{}, Status: "saved", CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	mock.ExpectExec("INSERT INTO image_request_archives").WithArgs(record.ID, int64(12), int64(34), nil, int64(7), "", "image", "", record.Endpoint, "prompt", "{}", "[]", "saved", "", int64(0), now, record.ExpiresAt).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Insert(ctx, record))
	mock.ExpectQuery("SELECT id::text FROM image_request_archives").WithArgs(now, 200).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(record.ID))
	ids, err := repo.Expired(ctx, now, 200)
	require.NoError(t, err)
	require.Equal(t, []string{record.ID}, ids)
	mock.ExpectExec("DELETE FROM image_request_archives WHERE id=\\$1").WithArgs(record.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Delete(ctx, record.ID))
	mock.ExpectQuery(regexp.QuoteMeta(imageArchiveSelect + ` WHERE a.id=$1`)).WithArgs(record.ID).WillReturnError(sql.ErrNoRows)
	_, err = repo.Get(ctx, record.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(719240300)`)).WillReturnError(fmt.Errorf("lock timeout"))
	mock.ExpectRollback()
	_, err = repo.Lock(ctx)
	require.ErrorContains(t, err, "lock timeout")
	require.NoError(t, mock.ExpectationsWereMet())
}
