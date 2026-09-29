package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountManagementCapabilitiesConcurrencyUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "not configured"},
		{name: "disabled", raw: `{"upgrade_enabled":false}`},
		{name: "initial configuration only", raw: `{"enabled":true,"group_ids":[7],"upgrade_enabled":false}`},
		{name: "enabled", raw: `{"upgrade_enabled":true,"upgrade_group_ids":[7]}`, want: true},
		{name: "invalid upgrade configuration", raw: `{"upgrade_enabled":true}`},
		{name: "malformed configuration", raw: `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &settingHandlerRepoStub{values: map[string]string{service.SettingKeyOAuthAutoConfig: tc.raw}}
			svc := service.NewSettingService(repo, &config.Config{})
			h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/management-capabilities", nil)
			h.GetAccountManagementCapabilities(c)
			require.Equal(t, http.StatusOK, rec.Code)
			var body struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, tc.want, body.Data["concurrency_upgrade_enabled"])
			require.Len(t, body.Data, 3, "only boolean capabilities are exposed to account managers")
			for _, value := range body.Data {
				require.IsType(t, false, value)
			}
		})
	}
}
