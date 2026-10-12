package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"strconv"
)

func TestExcelBPSModelPermissionForward(t *testing.T) {
	for _, code := range []string{"basispoints_model_access_changed", "model_not_allowed"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", code, stream), func(t *testing.T) {
				account := excelAccount()
				account.Credentials["model_mapping"] = map[string]any{"alias": "gpt-6-astra", "second-alias": "gpt-6-astra", "gpt-5.6-sol": "gpt-5.6-sol"}
				account.Extra["openai_excel_bps_models"] = []any{"gpt-6-astra", "gpt-5.6-sol"}
				account.Extra["openai_excel_bps_auto_disable_on_403"] = true
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"` + code + `"}}`))}}
				svc := openAIClientToolsTestService(upstream)
				svc.accountRepo = &excelBPSAutoDisableRepo{disable: func(context.Context, *Account) (bool, error) {
					t.Fatal("model rejection must not disable the account")
					return false, nil
				}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				_, err := svc.Forward(context.Background(), c, account, []byte(fmt.Sprintf(`{"model":"alias","input":"test","stream":%t}`, stream)))
				require.Error(t, err)
				require.True(t, svc.isExcelBPSModelCoolingDown(account, "alias"))
				require.True(t, svc.isExcelBPSModelCoolingDown(account, "second-alias"))
				require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "alias", false))
				require.False(t, svc.isExcelBPSModelCoolingDown(account, "gpt-5.6-sol"))
				require.Len(t, upstream.requests, 1)
				account.Extra["openai_excel_bps"] = false
				require.False(t, svc.isExcelBPSModelCoolingDown(account, "alias"))
			})
		}
	}
}

func TestExcelBPSModelCooldownExpiryAndBound(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := excelAccount()
	svc.coolDownExcelBPSModel(account, "gpt-6-astra")
	svc.excelBPSModelCooldowns.until[excelBPSModelKey{account.ID, "gpt-6-astra"}] = time.Now().Add(-time.Second)
	require.False(t, svc.isExcelBPSModelCoolingDown(account, "gpt-6-astra"))
	for i := 0; i < excelBPSModelPermissionMaxEntries+10; i++ {
		svc.coolDownExcelBPSModel(account, fmt.Sprint(i))
	}
	require.Len(t, svc.excelBPSModelCooldowns.until, excelBPSModelPermissionMaxEntries)
	require.False(t, isExcelBPSModelPermissionError(401, "model_not_allowed"))
	require.False(t, isExcelBPSModelPermissionError(403, "permission_denied"))
}

func TestExcelBPSModelPermissionSchedulers(t *testing.T) {
	for _, advanced := range []bool{true, false} {
		t.Run(fmt.Sprintf("advanced=%t", advanced), func(t *testing.T) {
			ctx := context.Background()
			groupID := int64(101301)
			newAccount := func(id int64) Account {
				return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"access_token": "token", "chatgpt_account_id": "chatgpt"},
					Extra:       map[string]any{"openai_excel_bps": true, "openai_excel_bps_models": []any{"gpt-6-astra"}}}
			}
			accounts := []Account{newAccount(38301), newAccount(38302)}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = true
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:              &schedulerTestGatewayCache{},
				cfg:                cfg,
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService(strconv.FormatBool(advanced)),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			require.Equal(t, advanced, svc.isOpenAIAdvancedSchedulerEnabled(ctx))
			selectModel := func(model string) (*AccountSelectionResult, error) {
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", model, nil, OpenAIUpstreamTransportAny, false)
				if selection != nil && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				return selection, err
			}

			svc.coolDownExcelBPSModel(&accounts[0], "gpt-6-astra")
			for range 10 {
				selection, err := selectModel("gpt-6-astra")
				require.NoError(t, err)
				require.Equal(t, int64(38302), selection.Account.ID)
			}

			svc.coolDownExcelBPSModel(&accounts[1], "gpt-6-astra")
			_, err := selectModel("gpt-6-astra")
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Contains(t, err.Error(), excelBPSModelPermissionFilterReason+"=2")
			selection, err := selectModel("gpt-5.1")
			require.NoError(t, err, "models these accounts forward natively stay schedulable")
			require.NotNil(t, selection.Account)
		})
	}
}
