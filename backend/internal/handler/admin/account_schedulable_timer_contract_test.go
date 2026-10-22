package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/accounttimer"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountTimerContractAdmin struct {
	service.AdminService
	actor          int64
	immediateCalls int
	bulkCalls      int
	updateCalls    int
	value          bool
}

func (s *accountTimerContractAdmin) UpdateAccount(ctx context.Context, id int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updateCalls++
	s.actor = accounttimer.ManualActor(ctx)
	return &service.Account{ID: id, Name: input.Name, Platform: service.PlatformAnthropic, Status: service.StatusActive}, nil
}
func (s *accountTimerContractAdmin) SetAccountSchedulable(ctx context.Context, id int64, value bool) (*service.Account, error) {
	s.immediateCalls++
	s.actor = accounttimer.ManualActor(ctx)
	s.value = value
	return &service.Account{ID: id, Platform: service.PlatformAnthropic, Schedulable: value, Status: service.StatusActive}, nil
}
func (s *accountTimerContractAdmin) BulkUpdateAccounts(ctx context.Context, input *service.BulkUpdateAccountsInput) (*service.BulkUpdateAccountsResult, error) {
	s.bulkCalls++
	s.actor = accounttimer.ManualActor(ctx)
	if input.Schedulable != nil {
		s.value = *input.Schedulable
	}
	return &service.BulkUpdateAccountsResult{Success: 1, SuccessIDs: input.AccountIDs}, nil
}
func TestAccountTimerHandlerUsesExistingSchedulingContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name, method, path, body string
		actor                    int64
		update, immediate, bulk  int
	}{
		{"ordinary edit", "PUT", "/accounts/7", `{"name":"after"}`, 0, 1, 0, 0},
		{"unknown schedulable is not an ordinary-edit contract", "PUT", "/accounts/7", `{"name":"after","schedulable":false}`, 0, 1, 0, 0},
		{"explicit immediate switch", "POST", "/accounts/7/schedulable", `{"schedulable":false}`, 19, 0, 1, 0},
		{"explicit bulk switch", "POST", "/accounts/bulk-update", `{"account_ids":[7],"schedulable":false}`, 19, 0, 0, 1},
		{"ordinary bulk edit", "POST", "/accounts/bulk-update", `{"account_ids":[7],"name":"after"}`, 0, 0, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &accountTimerContractAdmin{}
			h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			r := gin.New()
			r.Use(func(ctx *gin.Context) {
				ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 19})
				ctx.Set(string(middleware.ContextKeyUserRole), "admin")
			})
			r.PUT("/accounts/:id", h.Update)
			r.POST("/accounts/:id/schedulable", h.SetSchedulable)
			r.POST("/accounts/bulk-update", h.BulkUpdate)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, c.actor, svc.actor)
			require.Equal(t, c.update, svc.updateCalls)
			require.Equal(t, c.immediate, svc.immediateCalls)
			require.Equal(t, c.bulk, svc.bulkCalls)
			if c.actor > 0 {
				require.False(t, svc.value, "explicit false must not be confused with an omitted field")
			}
		})
	}
}
