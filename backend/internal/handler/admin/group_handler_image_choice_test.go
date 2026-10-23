package admin

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupHandlerPreservesExplicitImageGenerationChoice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	h := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{})
	r := gin.New()
	r.POST("/groups", h.Create)
	for _, body := range []string{
		`{"name":"images-off","platform":"gemini","rate_multiplier":1,"allow_image_generation":false}`,
		`{"name":"default","platform":"gemini","rate_multiplier":1}`,
	} {
		res := serveGroupRequest(r, http.MethodPost, "/groups", body)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	}
	require.Len(t, svc.createdGroups, 2)
	require.NotNil(t, svc.createdGroups[0].AllowImageGenerationExplicit)
	require.False(t, *svc.createdGroups[0].AllowImageGenerationExplicit)
	require.Nil(t, svc.createdGroups[1].AllowImageGenerationExplicit)
}
