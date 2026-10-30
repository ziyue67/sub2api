package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type customerHistoryRepository struct {
	service.ImageArchiveRepository
	records []service.ImageArchiveRecord
	filter  service.ImageArchiveFilter
}

func (r *customerHistoryRepository) Get(_ context.Context, id string) (*service.ImageArchiveRecord, error) {
	for _, v := range r.records {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (r *customerHistoryRepository) List(_ context.Context, f service.ImageArchiveFilter) ([]service.ImageArchiveRecord, int64, error) {
	r.filter = f
	return r.records, int64(len(r.records)), nil
}

func TestImageHistoryCustomerProjectionAllowlist(t *testing.T) {
	item := service.ImageArchiveRecord{
		ID: uuid.NewString(), UserID: 12, APIKeyID: 34, AccountID: 777, AccountName: "SECRET_ACCOUNT", UserEmail: "SECRET_EMAIL", APIKeyName: "SECRET_KEY", GroupName: "SECRET_GROUP",
		RequestID: "SECRET_UPSTREAM_ID", UpstreamModel: "SECRET_MODEL", Model: "public-image", Prompt: "SECRET_PROMPT", ErrorCodes: "SECRET_RAW_ERROR", Status: "failed",
		Parameters: map[string]any{"source": "SECRET_SOURCE", "_client_request_id": "client-visible", "_generation_status": "succeeded", "_delivery_status": "interrupted"},
		Files:      []service.ImageArchiveFile{{ID: 1, Role: "reference", RevisedPrompt: "SECRET_REFERENCE"}, {ID: 2, Role: "result", MIME: "image/jpeg", Thumbnail: true, PreviewOnly: true, RevisedPrompt: "SECRET_REVISION"}, {ID: 3, Role: "mask"}},
	}
	v := projectCustomerImageHistory(item, "/v1/images/history")
	payload, err := json.Marshal(v)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "SECRET_")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &fields))
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []string{"id", "request_id", "model", "status", "delivery_status", "message", "image_count", "files", "billing", "created_at", "expires_at"}, keys)
	require.Equal(t, "client-visible", v.RequestID)
	require.Equal(t, "succeeded", v.Status)
	require.Equal(t, "interrupted", v.DeliveryStatus)
	require.Equal(t, "unknown", v.Billing.Status)
	require.Nil(t, v.Billing.Amount)
	require.Len(t, v.Files, 1)
	require.True(t, v.Files[0].PreviewOnly)
	require.Equal(t, 2, v.Files[0].ID)
	require.Equal(t, "/v1/images/history/"+item.ID+"/files/2", v.Files[0].URL)
	// Storage failure alone does not prove generation failure.
	item.Parameters = nil
	item.Files = nil
	require.Equal(t, "unknown", projectCustomerImageHistory(item, "").Status)
}

func TestImageHistoryHandlerScopesAndDeniedIDs(t *testing.T) {
	own := service.ImageArchiveRecord{ID: uuid.NewString(), UserID: 12, APIKeyID: 34, Model: "public-image", ExpiresAt: time.Now().Add(time.Hour)}
	otherKey := own
	otherKey.ID = uuid.NewString()
	otherKey.APIKeyID = 35
	otherUser := own
	otherUser.ID = uuid.NewString()
	otherUser.UserID = 99
	repo := &customerHistoryRepository{records: []service.ImageArchiveRecord{own, otherKey, otherUser}}
	archive := service.NewImageArchiveService(repo, nil)
	defer archive.Stop()
	h := &OpenAIGatewayHandler{imageArchive: archive}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if c.GetHeader("Test-Auth") == "yes" {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 12})
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 34, UserID: 12})
		}
	})
	router.GET("/v1/images/history", h.ImageHistory)
	router.GET("/v1/images/history/:id", h.ImageHistoryDetail)
	router.GET("/v1/images/history/:id/files/:file", h.ImageHistoryFile)
	router.GET("/api/v1/user/image-history", h.UserImageHistory)
	request := func(path string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if auth {
			req.Header.Set("Test-Auth", "yes")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	w := request("/v1/images/history?api_key_id=35&user_id=99&page=2", true)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, int64(12), repo.filter.UserID)
	require.Equal(t, int64(34), repo.filter.APIKeyID)
	require.Equal(t, 2, repo.filter.Page)
	var page struct {
		Items []customerImageHistoryRecord `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, own.ID, page.Items[0].ID)
	w = request("/api/v1/user/image-history", true)
	require.Equal(t, 200, w.Code)
	require.Zero(t, repo.filter.APIKeyID)
	require.Equal(t, int64(12), repo.filter.UserID)
	for _, id := range []string{otherKey.ID, otherUser.ID, uuid.NewString(), "invalid"} {
		for _, suffix := range []string{"", "/files/1"} {
			w = request("/v1/images/history/"+id+suffix, true)
			require.Equal(t, 404, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "public-image")
		}
	}
	w = request("/v1/images/history/"+own.ID, true)
	require.Equal(t, 200, w.Code)
	for _, path := range []string{"/v1/images/history", "/v1/images/history/" + own.ID, "/v1/images/history/" + own.ID + "/files/1", "/api/v1/user/image-history"} {
		require.Equal(t, 401, request(path, false).Code)
	}
}

func TestImageHistoryHandlerUnavailable(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/v1/images/history", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 12})
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 34, UserID: 12})
	(&OpenAIGatewayHandler{}).ImageHistory(c)
	require.Equal(t, 503, w.Code)
}
