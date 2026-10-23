package cyberevidence

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSQLStoreEncryptedSaveIdempotentAndAtomic(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	s := NewSQLStore(db)
	for i := 0; i < 2; i++ {
		m.ExpectBegin()
		m.ExpectQuery("SELECT evidence_status.*FOR UPDATE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"evidence_status"}).AddRow("pending"))
		m.ExpectExec("INSERT INTO cyber_policy_evidence[\\s\\S]*ON CONFLICT\\(log_id\\) DO NOTHING").WithArgs(int64(7), "encrypted-synthetic", RedactionVersion, true, 9000, 7).WillReturnResult(sqlmock.NewResult(1, 1))
		m.ExpectExec("UPDATE content_moderation_logs SET evidence_status='saved'").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
		m.ExpectCommit()
		if e = s.Save(context.Background(), 7, Prepared{Ciphertext: "encrypted-synthetic", RedactionVersion: RedactionVersion, Truncated: true, OriginalBytes: 9000}, 7); e != nil {
			t.Fatal(e)
		}
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestSQLStoreDeletionWinsOverPendingWriter(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	s := NewSQLStore(db)
	m.ExpectBegin()
	m.ExpectQuery("SELECT evidence_status").WillReturnRows(sqlmock.NewRows([]string{"evidence_status"}).AddRow("deleted"))
	m.ExpectRollback()
	if !errors.Is(s.Save(context.Background(), 1, Prepared{Ciphertext: "encrypted-synthetic"}, 7), ErrNotFound) {
		t.Fatal("deleted evidence resurrected")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestSQLStoreFailureRollsBackAndMissingParent(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	s := NewSQLStore(db)
	m.ExpectBegin()
	m.ExpectQuery("SELECT evidence_status").WillReturnError(sql.ErrNoRows)
	m.ExpectRollback()
	if !errors.Is(s.Save(context.Background(), 1, Prepared{}, 7), ErrNotFound) {
		t.Fatal("orphan accepted")
	}
	m.ExpectBegin()
	m.ExpectQuery("SELECT evidence_status").WillReturnRows(sqlmock.NewRows([]string{"evidence_status"}).AddRow("pending"))
	m.ExpectExec("INSERT INTO cyber_policy_evidence").WillReturnError(errors.New("db unavailable"))
	m.ExpectRollback()
	if s.Save(context.Background(), 1, Prepared{Ciphertext: "encrypted"}, 7) == nil {
		t.Fatal("failed save reported success")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestSQLStoreExpiredReadNeverReturnsCiphertext(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	s := NewSQLStore(db)
	m.ExpectQuery("SELECT value FROM settings").WillReturnError(sql.ErrNoRows)
	m.ExpectQuery("SELECT l.id[\\s\\S]*e.expires_at>NOW()[\\s\\S]*u.deleted_at IS NULL").WithArgs(int64(1), 7).WillReturnRows(sqlmock.NewRows([]string{"id", "uid", "request", "endpoint", "model", "created", "status", "cipher", "expires"}).AddRow(1, 42, "req-synthetic", "/v1/chat/completions", "model", time.Now(), "expired", nil, time.Now().Add(-time.Hour)))
	r, e := s.Get(context.Background(), 1)
	if e != nil || r.Ciphertext != "" || r.Status != "expired" {
		t.Fatal("expired evidence exposed")
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestSQLStoreCleanupExpiryAndInterrupted(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	s := NewSQLStore(db)
	m.ExpectExec("WITH removed AS[\\s\\S]*expires_at<=NOW()[\\s\\S]*u.deleted_at IS NOT NULL").WithArgs(7).WillReturnResult(sqlmock.NewResult(0, 3))
	m.ExpectExec(regexp.QuoteMeta("UPDATE content_moderation_logs SET evidence_status='interrupted' WHERE evidence_status='pending' AND created_at<NOW()-INTERVAL '10 minutes'")).WillReturnResult(sqlmock.NewResult(0, 1))
	n, e := s.Cleanup(context.Background(), 7)
	if e != nil || n != 3 {
		t.Fatal("cleanup failed")
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
