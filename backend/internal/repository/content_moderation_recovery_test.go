package repository

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModerationRecoveryStaleTaskCannotReban(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &contentModerationRepository{db: db}
	m.ExpectBegin()
	m.ExpectQuery("SELECT status, role, moderation_status_revision FROM users.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"status", "role", "revision"}).AddRow("active", "user", 9))
	m.ExpectExec("INSERT INTO content_moderation_user_state").WithArgs(int64(306)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectQuery("SELECT version, generation, auto_ban_exempt, owned_status_revision, keyword_exempt, api_audit_exempt, email_exempt FROM content_moderation_user_state.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"version", "generation", "exempt", "owned", "keyword", "api", "email"}).AddRow(4, 2, false, nil, false, false, false))
	m.ExpectRollback()
	result, err := r.ApplyModerationHit(context.Background(), service.ModerationHit{UserID: 306, Version: 3, RequestID: "old-task", Since: time.Now(), AutoBan: true, Threshold: 1})
	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.False(t, result.BanApplied)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestModerationRecoveryUnbanDoesNotClaimOtherDisable(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &contentModerationRepository{db: db}
	m.ExpectBegin()
	m.ExpectQuery("SELECT status, role, moderation_status_revision FROM users.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"status", "role", "revision"}).AddRow("disabled", "user", 12))
	m.ExpectExec("INSERT INTO content_moderation_user_state").WithArgs(int64(306)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectQuery("SELECT version, generation, auto_ban_exempt, owned_status_revision, keyword_exempt, api_audit_exempt, email_exempt FROM content_moderation_user_state.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"version", "generation", "exempt", "owned", "keyword", "api", "email"}).AddRow(4, 2, false, 11, false, false, false))
	m.ExpectRollback()
	err = r.ManageModerationUser(context.Background(), 306, service.ModerationUserAction{Action: "unban", Version: 4, ActorID: 1, Reason: "reviewed"})
	require.ErrorContains(t, err, "MODERATION_DISABLE_NOT_OWNED")
	require.NoError(t, m.ExpectationsWereMet())
}

func expectModerationLock(m sqlmock.Sqlmock, status, role string, revision, version, generation int64, exempt bool, owned any) {
	m.ExpectBegin()
	m.ExpectQuery("SELECT status, role, moderation_status_revision FROM users.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"status", "role", "revision"}).AddRow(status, role, revision))
	m.ExpectExec("INSERT INTO content_moderation_user_state").WithArgs(int64(306)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectQuery("SELECT version, generation, auto_ban_exempt, owned_status_revision, keyword_exempt, api_audit_exempt, email_exempt FROM content_moderation_user_state.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"version", "generation", "exempt", "owned", "keyword", "api", "email"}).AddRow(version, generation, exempt, owned, false, false, false))
}

func TestModerationRecoveryThresholdOwnershipAndExemption(t *testing.T) {
	for _, tc := range []struct {
		name, status, role string
		exempt, wantBan    bool
	}{
		{"threshold", "active", "user", false, true},
		{"allowlisted still counts", "active", "user", true, false},
		{"administrator exempt", "active", "admin", false, false},
		{"other disable retained", "disabled", "user", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			r := &contentModerationRepository{db: db}
			expectModerationLock(m, tc.status, tc.role, 10, 2, 1, tc.exempt, nil)
			m.ExpectExec("INSERT INTO content_moderation_user_hits").WithArgs(int64(306), int64(1), "request-1", "keyword_block").WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectQuery("SELECT COUNT.*FROM content_moderation_user_hits").WithArgs(int64(306), int64(1), sqlmock.AnyArg(), false).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(10))
			if tc.wantBan {
				m.ExpectQuery("UPDATE users SET status='disabled'.*RETURNING moderation_status_revision").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(11))
				m.ExpectExec("UPDATE content_moderation_user_state SET owned_status_revision").WithArgs(int64(306), int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			m.ExpectCommit()
			result, err := r.ApplyModerationHit(context.Background(), service.ModerationHit{UserID: 306, Version: 2, RequestID: "request-1", Action: "keyword_block", Since: time.Now(), AutoBan: true, Threshold: 10})
			require.NoError(t, err)
			require.True(t, result.Accepted)
			require.Equal(t, 10, result.Count)
			require.Equal(t, tc.wantBan, result.BanApplied)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestModerationRecoveryDuplicateHitDoesNotNotifyOrBan(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &contentModerationRepository{db: db}
	expectModerationLock(m, "active", "user", 10, 2, 1, false, nil)
	m.ExpectExec("INSERT INTO content_moderation_user_hits").WithArgs(int64(306), int64(1), "duplicate", "keyword_block").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectRollback()
	result, err := r.ApplyModerationHit(context.Background(), service.ModerationHit{UserID: 306, Version: 2, RequestID: "duplicate", Action: "keyword_block"})
	require.NoError(t, err)
	require.False(t, result.Accepted)
	require.False(t, result.BanApplied)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestModerationRecoveryActionsRemainIndependent(t *testing.T) {
	for _, action := range []string{"unban", "reset", "exemption"} {
		t.Run(action, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			r := &contentModerationRepository{db: db}
			expectModerationLock(m, "disabled", "user", 11, 2, 1, false, 11)
			generation := int64(1)
			var owned any = int64(11)
			exempt := false
			in := service.ModerationUserAction{Action: action, Version: 2, ActorID: 7, Reason: "reviewed", Confirm: true}
			switch action {
			case "unban":
				m.ExpectQuery("UPDATE users SET status='active'.*RETURNING status").WithArgs(int64(306), int64(11)).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
				owned = nil
			case "reset":
				generation = 2
			case "exemption":
				exempt = true
				in.Exempt = &exempt
			}
			m.ExpectExec("UPDATE content_moderation_user_state SET version=version\\+1").WithArgs(int64(306), generation, exempt, owned, "reviewed", int64(7), false, false, false).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec("INSERT INTO content_moderation_user_actions").WithArgs(int64(306), int64(7), action, "reviewed", int64(3), exempt, false, false, false).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectCommit()
			require.NoError(t, r.ManageModerationUser(context.Background(), 306, in))
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestModerationRecoveryConcurrentAdminConflict(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &contentModerationRepository{db: db}
	expectModerationLock(m, "disabled", "user", 11, 3, 1, false, 11)
	m.ExpectRollback()
	err = r.ManageModerationUser(context.Background(), 306, service.ModerationUserAction{Action: "reset", Version: 2, ActorID: 7, Reason: "reviewed", Confirm: true})
	require.Error(t, err)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestModerationRecoveryTransactionFailureCannotSucceed(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := &contentModerationRepository{db: db}
	expectModerationLock(m, "active", "user", 10, 2, 1, false, nil)
	m.ExpectExec("INSERT INTO content_moderation_user_hits").WillReturnError(context.DeadlineExceeded)
	m.ExpectRollback()
	result, err := r.ApplyModerationHit(context.Background(), service.ModerationHit{UserID: 306, Version: 2, RequestID: "error"})
	require.Error(t, err)
	require.False(t, result.Accepted)
	require.False(t, result.BanApplied)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestModerationExemptionsTransactionAuditAndRevocation(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		for _, revoke := range []bool{false, true} {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			r := &contentModerationRepository{db: db}
			ban, keyword, api := mask&4 != 0, mask&1 != 0, mask&2 != 0
			m.ExpectBegin()
			m.ExpectQuery("SELECT status, role, moderation_status_revision FROM users.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"status", "role", "revision"}).AddRow("disabled", "user", 11))
			m.ExpectExec("INSERT INTO content_moderation_user_state").WithArgs(int64(306)).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery("SELECT version, generation, auto_ban_exempt, owned_status_revision, keyword_exempt, api_audit_exempt, email_exempt FROM.*FOR UPDATE").WithArgs(int64(306)).WillReturnRows(sqlmock.NewRows([]string{"version", "generation", "ban", "owned", "keyword", "api", "email"}).AddRow(2, 1, ban, 11, keyword, api, false))
			in := service.ModerationUserAction{Action: "exemption", Version: 2, ActorID: 7, Reason: "reviewed", Confirm: true}
			if revoke {
				keyword = false
				api = false
			} else {
				keyword = !keyword
				api = !api
			}
			in.KeywordExempt = &keyword
			in.APIAuditExempt = &api // Omitted legacy field must be preserved.
			m.ExpectExec("UPDATE content_moderation_user_state SET version=version\\+1").WithArgs(int64(306), int64(1), ban, int64(11), "reviewed", int64(7), keyword, api, false).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec("INSERT INTO content_moderation_user_actions.*auto_ban_exempt,keyword_exempt,api_audit_exempt").WithArgs(int64(306), int64(7), "exemption", "reviewed", int64(3), ban, keyword, api, false).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectCommit()
			require.NoError(t, r.ManageModerationUser(context.Background(), 306, in))
			require.NoError(t, m.ExpectationsWereMet())
			m.ExpectClose()
			require.NoError(t, db.Close())
		}
	}
}

func TestModerationExemptionsListReadbackAndFilters(t *testing.T) {
	for _, filter := range []string{"all", "exempt", "enforced", "auto_ban_exempt", "keyword_exempt", "api_audit_exempt", "email_exempt"} {
		db, m, err := sqlmock.New()
		require.NoError(t, err)
		r := &contentModerationRepository{db: db}
		m.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM users u JOIN content_moderation_user_state s ON s.user_id=u.id WHERE u.deleted_at IS NULL AND `+moderationExemptionFilter("$1")+` AND ($2::bigint=0 OR u.id=$2)`)).WithArgs(filter, int64(0)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		m.ExpectQuery(regexp.QuoteMeta(moderationUserProjection+`WHERE u.deleted_at IS NULL AND s.user_id IS NOT NULL AND `+moderationExemptionFilter("$3")+` AND ($4::bigint=0 OR u.id=$4) ORDER BY s.updated_at DESC,u.id DESC LIMIT $5 OFFSET $6`)).WithArgs(sqlmock.AnyArg(), false, filter, int64(0), 20, 0).WillReturnRows(sqlmock.NewRows([]string{"id", "version", "generation", "ban", "keyword", "api", "email", "owned", "status", "count", "reason", "actor", "time"}).AddRow(306, 3, 1, true, true, false, true, true, "disabled", 10, "reviewed", 7, nil))
		items, total, err := r.ListModerationUsers(context.Background(), 1, 20, filter, 0, time.Now(), false)
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, items, 1)
		require.True(t, items[0].AutoBanExempt)
		require.True(t, items[0].KeywordExempt)
		require.False(t, items[0].APIAuditExempt)
		require.True(t, items[0].EmailExempt)
		require.NoError(t, m.ExpectationsWereMet())
		m.ExpectClose()
		require.NoError(t, db.Close())
	}
}

func TestModerationExemptionsAuditFailureRollsBack(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	r := &contentModerationRepository{db: db}
	expectModerationLock(m, "active", "user", 10, 2, 1, false, nil)
	keyword := true
	in := service.ModerationUserAction{Action: "exemption", Version: 2, ActorID: 7, Reason: "reviewed", Confirm: true, KeywordExempt: &keyword}
	m.ExpectExec("UPDATE content_moderation_user_state SET version=version\\+1").WithArgs(int64(306), int64(1), false, nil, "reviewed", int64(7), true, false, false).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO content_moderation_user_actions").WillReturnError(context.DeadlineExceeded)
	m.ExpectRollback()
	require.ErrorIs(t, r.ManageModerationUser(context.Background(), 306, in), context.DeadlineExceeded)
	require.NoError(t, m.ExpectationsWereMet())
	m.ExpectClose()
	require.NoError(t, db.Close())
}

func TestModerationEmailExemptionTransactionPreservesOmittedFlags(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		for _, action := range []string{"email-only", "legacy", "reset"} {
			t.Run(fmt.Sprintf("mask%d/%s", mask, action), func(t *testing.T) {
				db, m, err := sqlmock.New()
				require.NoError(t, err)
				defer func() { _ = db.Close() }()
				r := &contentModerationRepository{db: db}
				ban, keyword, api, email := mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0
				m.ExpectBegin()
				m.ExpectQuery("SELECT status, role, moderation_status_revision FROM users.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"status", "role", "revision"}).AddRow("disabled", "user", 11))
				m.ExpectExec("INSERT INTO content_moderation_user_state").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 0))
				m.ExpectQuery("SELECT version, generation, auto_ban_exempt, owned_status_revision, keyword_exempt, api_audit_exempt, email_exempt FROM.*FOR UPDATE").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"version", "generation", "ban", "owned", "keyword", "api", "email"}).AddRow(2, 1, ban, 11, keyword, api, email))
				in := service.ModerationUserAction{Action: "exemption", Version: 2, ActorID: 7, Reason: "reviewed", Confirm: true}
				generation := int64(1)
				switch action {
				case "email-only":
					email = !email
					in.EmailExempt = &email
				case "legacy":
					ban = !ban
					in.Exempt = &ban
				case "reset":
					in.Action = "reset"
					generation++
				}
				m.ExpectExec(regexp.QuoteMeta(`UPDATE content_moderation_user_state SET version=version+1,generation=$2,auto_ban_exempt=$3,owned_status_revision=$4,reason=$5,actor_id=$6,keyword_exempt=$7,api_audit_exempt=$8,email_exempt=$9,updated_at=NOW() WHERE user_id=$1`)).WithArgs(int64(42), generation, ban, int64(11), "reviewed", int64(7), keyword, api, email).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(regexp.QuoteMeta(`INSERT INTO content_moderation_user_actions(user_id,actor_id,action,reason,version,auto_ban_exempt,keyword_exempt,api_audit_exempt,email_exempt) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`)).WithArgs(int64(42), int64(7), in.Action, "reviewed", int64(3), ban, keyword, api, email).WillReturnResult(sqlmock.NewResult(1, 1))
				m.ExpectCommit()
				require.NoError(t, r.ManageModerationUser(context.Background(), 42, in))
				require.NoError(t, m.ExpectationsWereMet())
			})
		}
	}
}
