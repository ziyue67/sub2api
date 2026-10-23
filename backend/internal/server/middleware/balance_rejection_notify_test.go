//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type authBalanceRecorder struct{ balances []float64 }

func (r *authBalanceRecorder) CheckBalanceOnRejection(_ *service.User, balance float64) {
	r.balances = append(r.balances, balance)
}

func TestBalanceRejectionNotificationAcrossAuthProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, google := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			balance float64
			status  string
			path    string
			want    int
			notify  bool
		}{
			{"exhausted", 0, service.StatusActive, "/v1/responses", 403, true},
			{"negative", -1, service.StatusActive, "/v1/messages", 403, true},
			{"eligible", 1, service.StatusActive, "/v1/chat/completions", 200, false},
			{"key quota", 0, service.StatusAPIKeyQuotaExhausted, "/v1/responses", 429, false},
		} {
			name := tc.name
			if google {
				name += "/google"
			}
			t.Run(name, func(t *testing.T) {
				cfg := &config.Config{RunMode: config.RunModeStandard}
				svc := service.NewAPIKeyService(&stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) {
					return &service.APIKey{ID: 81, UserID: 71, Status: tc.status, User: &service.User{ID: 71, Status: service.StatusActive, Balance: tc.balance}}, nil
				}}, nil, nil, nil, nil, nil, cfg)
				recorder := &authBalanceRecorder{}
				svc.SetBalanceRejectionNotifier(recorder)
				router := gin.New()
				if google {
					router.Use(APIKeyAuthGoogle(svc, cfg))
				} else {
					router.Use(apiKeyAuthWithSubscription(svc, nil, cfg))
				}
				router.POST(tc.path, func(c *gin.Context) { c.Status(200) })
				req := httptest.NewRequest(http.MethodPost, tc.path, nil)
				req.Header.Set("Authorization", "Bearer synthetic-key")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				require.Equal(t, tc.want, response.Code)
				if tc.notify {
					require.Equal(t, []float64{tc.balance}, recorder.balances)
				} else {
					require.Empty(t, recorder.balances)
				}
			})
		}
	}
}
