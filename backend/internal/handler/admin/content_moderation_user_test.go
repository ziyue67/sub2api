package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Complete the runtime read contract as well as the handler's GetValue path.
// The production constructor starts workers even when the feature is disabled.
type moderationUserHandlerSettings struct{ moderationEngineSettings }

func (*moderationUserHandlerSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = ""
	}
	values[service.SettingKeyRiskControlEnabled] = "false"
	return values, nil
}

type moderationUserHandlerRepo struct {
	service.ContentModerationRepository
	state   service.ModerationUserState
	actions []service.ModerationUserAction
	reads   int
}

func (*moderationUserHandlerRepo) CleanupExpiredLogs(context.Context, time.Time, time.Time) (*service.ContentModerationCleanupResult, error) {
	return &service.ContentModerationCleanupResult{}, nil
}
func (r *moderationUserHandlerRepo) GetModerationUser(context.Context, int64, time.Time, bool) (*service.ModerationUserState, error) {
	r.reads++
	copy := r.state
	return &copy, nil
}
func (r *moderationUserHandlerRepo) ListModerationUsers(context.Context, int, int, string, int64, time.Time, bool) ([]service.ModerationUserState, int64, error) {
	r.reads++
	return []service.ModerationUserState{r.state}, 1, nil
}
func (r *moderationUserHandlerRepo) ManageModerationUser(_ context.Context, _ int64, in service.ModerationUserAction) error {
	r.actions = append(r.actions, in)
	r.state.Version++
	if in.Exempt != nil {
		r.state.AutoBanExempt = *in.Exempt
	}
	if in.KeywordExempt != nil {
		r.state.KeywordExempt = *in.KeywordExempt
	}
	if in.APIAuditExempt != nil {
		r.state.APIAuditExempt = *in.APIAuditExempt
	}
	if in.EmailExempt != nil {
		r.state.EmailExempt = *in.EmailExempt
	}
	return nil
}
func (r *moderationUserHandlerRepo) ApplyModerationHit(context.Context, service.ModerationHit) (service.ModerationHitResult, error) {
	panic("unexpected hit")
}

func moderationUserRequest(t *testing.T, h *ContentModerationHandler, method, path, role, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if role != "" {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 17})
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
		c.Next()
	})
	r.GET("/users", h.ListUsers)
	r.GET("/users/:user_id", h.GetUser)
	r.POST("/users/:user_id/actions", h.ManageUser)
	r.POST("/users/:user_id/unban", h.UnbanUser)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func TestModerationUserHandlerAuthority(t *testing.T) {
	for _, role := range []string{"", "user", "observer"} {
		repo := &moderationUserHandlerRepo{}
		h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, nil, nil))
		for _, path := range []string{"/users", "/users/306"} {
			require.Equal(t, http.StatusForbidden, moderationUserRequest(t, h, "GET", path, role, "").Code)
		}
		for _, path := range []string{"/users/306/actions", "/users/306/unban"} {
			require.Equal(t, http.StatusForbidden, moderationUserRequest(t, h, "POST", path, role, `{"action":"reset","version":0,"confirm":true,"reason":"reviewed"}`).Code)
		}
		require.Zero(t, repo.reads)
		require.Empty(t, repo.actions)
	}
}
func TestModerationUserHandlerValidationAndActor(t *testing.T) {
	repo := &moderationUserHandlerRepo{state: service.ModerationUserState{UserID: 306, Status: "disabled"}}
	h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, nil, nil))
	for _, body := range []string{`{}`, `{"action":"reset","confirm":true,"reason":"review"}`, `{"action":"reset","version":0,"confirm":false,"reason":"review"}`, `{"action":"exemption","version":0,"confirm":true,"reason":"review"}`, `{"action":"reset","version":-1,"confirm":true,"reason":"review"}`, `{"action":"unban","version":0,"confirm":true,"reason":"   "}`, `{"action":"reset","version":0,"confirm":true,"reason":"` + strings.Repeat("字", 501) + `"}`, `{"action":"exemption","version":0,"confirm":true,"reason":"review","auto_ban_exempt":"yes"}`} {
		w := moderationUserRequest(t, h, "POST", "/users/306/actions", "admin", body)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	require.Empty(t, repo.actions)
	w := moderationUserRequest(t, h, "POST", "/users/306/actions", "admin", `{"action":"reset","version":0,"confirm":true,"reason":"review","actor_id":999}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Len(t, repo.actions, 1)
	require.Equal(t, int64(17), repo.actions[0].ActorID)
	require.Contains(t, w.Body.String(), `"status":"disabled"`)
	require.Contains(t, w.Body.String(), `"version":1`)
}
func TestModerationUserHandlerListFilterPrivacy(t *testing.T) {
	repo := &moderationUserHandlerRepo{state: service.ModerationUserState{UserID: 306, AutoBanExempt: true, Reason: "review", Status: "active"}}
	h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, nil, nil))
	for _, query := range []string{"?page=0", "?page=oops", "?page_size=101", "?page_size=no", "?state=unknown", "?user_id=-1", "?user_id=no"} {
		w := moderationUserRequest(t, h, "GET", "/users"+query, "admin", "")
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	w := moderationUserRequest(t, h, "GET", "/users?state=exempt&page=1&page_size=20", "admin", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"user_id":306`)
	// email_exempt is a boolean control, not a mailbox. Check the complete
	// response shape so actual email/body/credential fields remain forbidden.
	keys := func(object map[string]json.RawMessage) []string {
		result := make([]string, 0, len(object))
		for key := range object {
			result = append(result, key)
		}
		return result
	}
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.ElementsMatch(t, []string{"code", "message", "data"}, keys(envelope))
	var page map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["data"], &page))
	require.ElementsMatch(t, []string{"items", "total", "page", "page_size", "pages"}, keys(page))
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(page["items"], &items))
	require.Len(t, items, 1)
	require.ElementsMatch(t, []string{
		"user_id", "version", "generation", "auto_ban_exempt", "keyword_exempt",
		"api_audit_exempt", "email_exempt", "owned_disable", "status",
		"current_count", "reason", "actor_id", "updated_at",
	}, keys(items[0]))
	require.JSONEq(t, "false", string(items[0]["email_exempt"]))
	require.NotContains(t, w.Body.String(), "prompt")
	require.NotContains(t, w.Body.String(), "api_key")
}

func TestModerationUserHandlerRuntimeDependencyContract(t *testing.T) {
	repo := &moderationUserHandlerRepo{}
	settings := &moderationUserHandlerSettings{}
	svc := service.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil, nil)
	status, err := svc.GetStatus(context.Background())
	require.NoError(t, err)
	require.False(t, status.RiskControlEnabled)
	cleanup, err := repo.CleanupExpiredLogs(context.Background(), time.Now(), time.Now())
	require.NoError(t, err)
	require.NotNil(t, cleanup)
}

type moderationUserFailingInvalidator struct {
	service.APIKeyAuthCacheInvalidator
}

func (*moderationUserFailingInvalidator) InvalidateAuthCacheByUserIDChecked(context.Context, int64) error {
	return errors.New("mock cache unavailable")
}
func TestModerationUserHandlerCacheFailureCannotReportSuccess(t *testing.T) {
	repo := &moderationUserHandlerRepo{state: service.ModerationUserState{UserID: 306, Status: "active"}}
	h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, &moderationUserFailingInvalidator{}, nil))
	w := moderationUserRequest(t, h, "POST", "/users/306/actions", "admin", `{"action":"reset","version":0,"confirm":true,"reason":"reviewed"}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "MODERATION_CACHE_INVALIDATION_FAILED")
	require.Contains(t, w.Body.String(), "Action committed")
	require.Len(t, repo.actions, 1)
	require.Zero(t, repo.reads)
}

func TestModerationUserHandlerIndependentExemptions(t *testing.T) {
	for _, field := range []string{"keyword_exempt", "api_audit_exempt", "email_exempt"} {
		repo := &moderationUserHandlerRepo{state: service.ModerationUserState{UserID: 306, Status: "disabled", CurrentCount: 10}}
		h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, nil, nil))
		body := `{"action":"exemption","version":0,"confirm":true,"reason":"review","` + field + `":true,"actor_id":999}`
		for _, role := range []string{"", "user", "observer"} {
			require.Equal(t, 403, moderationUserRequest(t, h, "POST", "/users/306/actions", role, body).Code)
		}
		require.Empty(t, repo.actions)
		w := moderationUserRequest(t, h, "POST", "/users/306/actions", "admin", body)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Len(t, repo.actions, 1)
		require.Equal(t, int64(17), repo.actions[0].ActorID)
		raw, err := json.Marshal(repo.actions[0])
		require.NoError(t, err)
		require.Contains(t, string(raw), `"`+field+`":true`)
		require.Contains(t, w.Body.String(), `"status":"disabled"`)
		for _, invalid := range []string{
			`{"action":"exemption","version":1,"confirm":true,"reason":"review","` + field + `":"yes"}`,
			`{"action":"reset","version":1,"confirm":true,"reason":"review","` + field + `":true}`,
		} {
			require.Equal(t, 400, moderationUserRequest(t, h, "POST", "/users/306/actions", "admin", invalid).Code)
		}
	}
	repo := &moderationUserHandlerRepo{}
	h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, nil, nil))
	for _, filter := range []string{"auto_ban_exempt", "keyword_exempt", "api_audit_exempt", "email_exempt"} {
		w := moderationUserRequest(t, h, "GET", "/users?state="+filter, "admin", "")
		require.Equal(t, 200, w.Code, w.Body.String())
	}
}

func TestModerationEmailExemptionHandlerReadbackAndLegacyOmission(t *testing.T) {
	repo := &moderationUserHandlerRepo{state: service.ModerationUserState{UserID: 42, EmailExempt: true, AutoBanExempt: true, KeywordExempt: true, APIAuditExempt: true, CurrentCount: 10, Status: "disabled"}}
	h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, nil, nil))
	w := moderationUserRequest(t, h, "POST", "/users/42/actions", "admin", `{"action":"exemption","version":0,"confirm":true,"reason":"legacy update","auto_ban_exempt":false}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Nil(t, repo.actions[0].EmailExempt)
	require.Contains(t, w.Body.String(), `"auto_ban_exempt":false`)
	require.Contains(t, w.Body.String(), `"keyword_exempt":true`)
	require.Contains(t, w.Body.String(), `"api_audit_exempt":true`)
	require.Contains(t, w.Body.String(), `"email_exempt":true`)
	w = moderationUserRequest(t, h, "POST", "/users/42/actions", "admin", `{"action":"exemption","version":1,"confirm":true,"reason":"revoke mail only","email_exempt":false}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotNil(t, repo.actions[1].EmailExempt)
	require.False(t, *repo.actions[1].EmailExempt)
	require.Nil(t, repo.actions[1].Exempt)
	require.Nil(t, repo.actions[1].KeywordExempt)
	require.Nil(t, repo.actions[1].APIAuditExempt)
	require.Contains(t, w.Body.String(), `"email_exempt":false`)
	require.Contains(t, w.Body.String(), `"auto_ban_exempt":false`)
	require.Contains(t, w.Body.String(), `"keyword_exempt":true`)
	require.Contains(t, w.Body.String(), `"api_audit_exempt":true`)
	require.Contains(t, w.Body.String(), `"current_count":10`)
	require.Contains(t, w.Body.String(), `"status":"disabled"`)
}

func TestModerationEmailExemptionHandlerCacheFailureCannotReportSuccess(t *testing.T) {
	repo := &moderationUserHandlerRepo{state: service.ModerationUserState{UserID: 42, Status: "active"}}
	h := NewContentModerationHandler(service.NewContentModerationService(&moderationUserHandlerSettings{}, repo, nil, nil, nil, nil, &moderationUserFailingInvalidator{}, nil))
	w := moderationUserRequest(t, h, "POST", "/users/42/actions", "admin", `{"action":"exemption","version":0,"confirm":true,"reason":"mail only","email_exempt":true}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "MODERATION_CACHE_INVALIDATION_FAILED")
	require.True(t, repo.state.EmailExempt)
	require.Len(t, repo.actions, 1)
	require.Zero(t, repo.reads)
}
