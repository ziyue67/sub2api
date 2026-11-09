//go:build unit

package routes

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUnifiedRoutingHTTPAuthenticationAndDiscovery(t *testing.T) {
	for _, scenario := range []string{"priority", "lowest-rate", "disabled-primary", "all-disabled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUnionFixture()
			p := &domain.APIKeyGroupRouting{Strategy: service.APIKeyRoutingPriority, Routes: []domain.APIKeyGroupRoute{{GroupID: 1, Priority: 1, Weight: 1, Enabled: true}, {GroupID: 2, Priority: 2, Weight: 1, Enabled: true}}}
			f.key.GroupRouting = p
			wantID := int64(1)
			wantModels := []string{"image-model", "shared-model", "text-model"}
			switch scenario {
			case "lowest-rate":
				p.Strategy = service.APIKeyRoutingLowestRate
				wantID = 2
			case "disabled-primary":
				p.Routes[0].Enabled = false
				wantID = 2
				wantModels = []string{"image-model", "shared-model"}
			case "all-disabled":
				p.Routes[0].Enabled = false
				p.Routes[1].Enabled = false
			}
			router := f.router(t, "", nil, nil)
			listing := unionRequest(router, "GET", "/v1/models")
			admission := unionRequest(router, "POST", "/union-auth")
			if scenario == "all-disabled" {
				require.Equal(t, 403, listing.Code, listing.Body.String())
				require.Equal(t, 403, admission.Code, admission.Body.String())
				return
			}
			require.Equal(t, wantModels, unionIDs(t, listing))
			require.Equal(t, 200, admission.Code, admission.Body.String())
			var body struct {
				GroupID int64   `json:"group_id"`
				Rate    float64 `json:"rate"`
			}
			require.NoError(t, json.Unmarshal(admission.Body.Bytes(), &body))
			require.Equal(t, wantID, body.GroupID)
			require.Equal(t, f.groups.groups[wantID].RateMultiplier, body.Rate)
			require.Equal(t, int64(1), *f.key.GroupID, "request must not mutate the auth repository snapshot")
		})
	}
}
