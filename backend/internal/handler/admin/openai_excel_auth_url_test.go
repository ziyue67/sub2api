package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAdminAuthURLSelectsExcel(t *testing.T) {
	svc := service.NewOpenAIOAuthService(nil, nil)
	defer svc.Stop()
	handler := &OpenAIOAuthHandler{openaiOAuthService: svc}
	for _, body := range []string{`{"oauth_client":"excel"}`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/generate-auth-url", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		handler.GenerateAuthURL(c)
		require.Equal(t, http.StatusOK, w.Code)
		var response struct {
			Data struct {
				AuthURL string `json:"auth_url"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		u, err := url.Parse(response.Data.AuthURL)
		require.NoError(t, err)
		require.Equal(t, openai.ExcelClientID, u.Query().Get("client_id"))
		require.Equal(t, openai.ExcelRedirectURI, u.Query().Get("redirect_uri"))
		require.Empty(t, u.Query().Get("codex_cli_simplified_flow"))
	}
}
