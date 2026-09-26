package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type excelBPSGroupActionRepo struct {
	excelBPSAutoDisableRepo
	move func(context.Context, *Account) (bool, error)
}

func (r *excelBPSGroupActionRepo) MoveExcelBPSOn403(ctx context.Context, a *Account) (bool, error) {
	return r.move(ctx, a)
}

func TestExcelBPS403GroupTarget(t *testing.T) {
	_, enabled := (*Account)(nil).ExcelBPS403GroupTarget()
	require.False(t, enabled)
	for _, value := range []any{nil, true, "0", -1, 1.5, math.NaN(), math.Inf(1), float64(math.MaxInt64), json.Number("-1")} {
		account := excelAccount()
		account.Extra[ExcelBPSAutoMoveOn403Key] = true
		account.Extra[ExcelBPS403TargetGroupIDKey] = value
		_, enabled := account.ExcelBPS403GroupTarget()
		require.False(t, enabled, "invalid destination must never become leave-all: %v", value)
	}
	for _, value := range []any{0, int64(0), float64(0), json.Number("0"), 7, int64(7), float64(7), json.Number("7")} {
		account := excelAccount()
		account.Extra[ExcelBPSAutoMoveOn403Key] = true
		account.Extra[ExcelBPS403TargetGroupIDKey] = value
		_, enabled := account.ExcelBPS403GroupTarget()
		require.True(t, enabled)
		account.Extra["openai_excel_bps"] = false
		_, enabled = account.ExcelBPS403GroupTarget()
		require.False(t, enabled)
	}
}

func TestExcelBPS403GroupActionTrigger(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		status                            int
		target                            int64
		disabled, optIn, modelError, fail bool
	}{
		{name: "default off", status: 403},
		{name: "move", status: 403, optIn: true, target: 7},
		{name: "leave all", status: 403, optIn: true},
		{name: "both switches", status: 403, optIn: true, target: 7, disabled: true},
		{name: "group write failure still disables", status: 403, optIn: true, disabled: true, fail: true},
		{name: "model access", status: 403, optIn: true, modelError: true},
		{name: "unauthorized", status: 401, optIn: true},
		{name: "rate limited", status: 429, optIn: true},
		{name: "server error", status: 500, optIn: true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				account := excelAccount()
				account.GroupIDs = []int64{1, 2}
				account.Extra[ExcelBPSAutoMoveOn403Key] = tc.optIn
				account.Extra[ExcelBPS403TargetGroupIDKey] = tc.target
				account.Extra["openai_excel_bps_auto_disable_on_403"] = tc.disabled
				raw := "{\"error\":{\"code\":\"permission_denied\"}}"
				if tc.modelError {
					raw = "{\"error\":{\"code\":\"basispoints_model_access_changed\"}}"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}}
				svc := openAIClientToolsTestService(upstream)
				var actions []string
				svc.accountRepo = &excelBPSGroupActionRepo{
					excelBPSAutoDisableRepo: excelBPSAutoDisableRepo{disable: func(context.Context, *Account) (bool, error) { actions = append(actions, "disable"); return true, nil }},
					move: func(ctx context.Context, got *Account) (bool, error) {
						actions = append(actions, "move")
						require.NoError(t, ctx.Err())
						_, bounded := ctx.Deadline()
						require.True(t, bounded)
						target, enabled := got.ExcelBPS403GroupTarget()
						require.True(t, enabled)
						require.Equal(t, tc.target, target)
						if tc.fail {
							return false, errors.New("write failed")
						}
						return true, nil
					},
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				_, err := svc.Forward(context.Background(), c, account, []byte(fmt.Sprintf("{\"model\":\"gpt-6-astra\",\"input\":\"test\",\"stream\":%t}", stream)))
				require.Error(t, err)
				triggered := tc.status == 403 && tc.optIn && !tc.modelError
				var want []string
				if triggered {
					want = append(want, "move")
				}
				if tc.status == 403 && tc.disabled && !tc.modelError {
					want = append(want, "disable")
				}
				require.Equal(t, want, actions)
				require.Equal(t, triggered && !tc.fail, strings.Contains(rec.Body.String(), "account groups were"))
				require.Len(t, upstream.requests, 1)
				require.Equal(t, []int64{1, 2}, account.GroupIDs)
				require.True(t, account.IsExcelBPSEnabled())
			})
		}
	}
}

func TestExcelBPS403GroupActionDuringCorrection(t *testing.T) {
	for _, modelAccess := range []bool{false, true} {
		t.Run(fmt.Sprint(modelAccess), func(t *testing.T) {
			raw := "{\"error\":{\"code\":\"permission_denied\"}}"
			if modelAccess {
				raw = "{\"error\":{\"code\":\"basispoints_model_access_changed\"}}"
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(excelBPSRepairWire(t, "group", "Run", "text(42);")))},
				{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))},
			}}
			account := excelAccount()
			account.Extra[ExcelBPSAutoMoveOn403Key] = true
			account.Extra[ExcelBPS403TargetGroupIDKey] = 0
			svc := openAIClientToolsTestService(upstream)
			calls := 0
			svc.accountRepo = &excelBPSGroupActionRepo{move: func(context.Context, *Account) (bool, error) { calls++; return true, nil }}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			_, err := svc.Forward(context.Background(), c, account, []byte("{\"model\":\"gpt-6-astra\",\"stream\":true,\"input\":\"test\",\"tools\":[{\"type\":\"custom\",\"name\":\"exec\"}]}"))
			require.Error(t, err)
			require.Len(t, upstream.requests, 2)
			if modelAccess {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

type excelBPSDestinationRepo struct {
	GroupRepository
	group *Group
	err   error
}

func (r *excelBPSDestinationRepo) GetByIDLite(context.Context, int64) (*Group, error) {
	return r.group, r.err
}

func TestValidateExcelBPS403GroupSettings(t *testing.T) {
	for _, tc := range []struct {
		name, platform           string
		target                   any
		simple, missing, wantErr bool
	}{
		{name: "leave all", target: 0},
		{name: "openai", platform: PlatformOpenAI, target: 7},
		{name: "composite", platform: PlatformComposite, target: 7},
		{name: "simple composite", platform: PlatformComposite, target: 7, simple: true, wantErr: true},
		{name: "incompatible platform", platform: PlatformAnthropic, target: 7, wantErr: true},
		{name: "missing group", target: 7, missing: true, wantErr: true},
		{name: "missing target", wantErr: true},
		{name: "string zero", target: "0", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := excelAccount()
			account.Extra[ExcelBPSAutoMoveOn403Key] = true
			account.Extra[ExcelBPS403TargetGroupIDKey] = tc.target
			groups := &excelBPSDestinationRepo{group: &Group{ID: 7, Platform: tc.platform}}
			if tc.missing {
				groups.err = ErrGroupNotFound
			}
			svc := &adminServiceImpl{groupRepo: groups, cfg: &config.Config{}}
			if tc.simple {
				svc.cfg.RunMode = config.RunModeSimple
			}
			err := svc.validateExcelBPS403GroupSettings(context.Background(), account)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
