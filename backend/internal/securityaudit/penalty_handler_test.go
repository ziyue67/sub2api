package securityaudit

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPersonalSecurityRecordCannotOverrideAuthenticatedOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	h := ProvidePenaltyAdminHandler(nil, service.NewSecurityPenaltyService(db, nil, nil))
	r := gin.New()
	r.GET("/security/events/:id", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	}, h.MyPenaltyEvent)
	// The authenticated user is 7 even when the query asks for owner 999.
	mock.ExpectQuery(`SELECT .*full_prompt.* FROM security_penalty_events WHERE id=\$1 AND \(\$2::bigint=0 OR user_id=\$2\)`).WithArgs(int64(12), int64(7)).WillReturnError(sql.ErrNoRows)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/security/events/12?user_id=999", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

type ipAuditPenaltySettings struct{ service.SettingRepository }

func (ipAuditPenaltySettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestIPAuditPenaltyAdapterUsesExistingUserWideTransactionAndReadsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	h := ProvidePenaltyAdminHandler(nil, service.NewSecurityPenaltyService(db, ipAuditPenaltySettings{}, nil))
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE security_penalty_subjects SET strike_count=0").WithArgs(int64(9)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO security_penalty_releases").WithArgs(int64(9), int64(17), "operator_review").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .* FROM security_penalty_subjects WHERE user_id").WithArgs(int64(9), "session_block").WillReturnRows(sqlmock.NewRows([]string{"user_id", "scope", "subject_key", "policy", "strike_count", "window_started_at", "blocked_until", "route_until", "updated_at"}))
	summary, err := h.ReleaseIPAuditPenalty(context.Background(), 9, 17, "operator_review")
	require.NoError(t, err)
	require.Empty(t, summary.States)
	require.False(t, summary.Restricted)
	require.NoError(t, mock.ExpectationsWereMet())
}
