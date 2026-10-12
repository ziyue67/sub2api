//go:build unit

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// excelBPSImageAdmissionAccountRepo records every account a forward attempt
// was admitted on.
type excelBPSImageAdmissionAccountRepo struct {
	excelBPSFailoverAccountRepo
	attempts *excelBPSImageAdmissionAttempts
}

type excelBPSImageAdmissionAttempts struct {
	mu  sync.Mutex
	ids []int64
}

func (a *excelBPSImageAdmissionAttempts) since(n int) []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int64(nil), a.ids[n:]...)
}

func (a *excelBPSImageAdmissionAttempts) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.ids)
}

func (r excelBPSImageAdmissionAccountRepo) GetOpenAITurnAdmission(ctx context.Context, id int64) (*service.Account, *service.Account, error) {
	r.attempts.mu.Lock()
	r.attempts.ids = append(r.attempts.ids, id)
	r.attempts.mu.Unlock()
	return r.excelBPSFailoverAccountRepo.GetOpenAITurnAdmission(ctx, id)
}

// excelBPSImageAdmissionSettingRepo enables image support with a single
// in-flight BPS request.
type excelBPSImageAdmissionSettingRepo struct {
	service.SettingRepository
}

func (excelBPSImageAdmissionSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := map[string]string{
		service.SettingKeyExcelBPSImageRelayEnabled: "true",
		service.SettingKeyExcelBPSImageBaseURL:      "https://images.example",
		service.SettingKeyExcelBPSImageMaxRequests:  "1",
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (excelBPSImageAdmissionSettingRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}

// The lower priority value is scheduled first.
func excelBPSImageAdmissionAccount(id, groupID int64, bps bool, priority int) service.Account {
	extra := map[string]any{}
	if bps {
		extra["openai_excel_bps"] = true
	}
	return service.Account{
		ID: id, Name: fmt.Sprintf("account-%d", id), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID}, Priority: priority,
		Credentials: map[string]any{"access_token": fmt.Sprintf("token-%d", id), "chatgpt_account_id": fmt.Sprintf("chatgpt-%d", id)},
		Extra:       extra,
	}
}

func newExcelBPSImageAdmissionTestHandler(t *testing.T, upstream service.HTTPUpstream, loadBatch bool, accounts []service.Account) (*OpenAIGatewayHandler, *excelBPSImageAdmissionAttempts) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	concurrencyService := service.NewConcurrencyService(nil)
	var schedulingConcurrency *service.ConcurrencyService
	if loadBatch {
		cfg.Gateway.Scheduling.LoadBatchEnabled = true
		schedulingConcurrency = concurrencyService
	}
	settingService := service.NewSettingService(excelBPSImageAdmissionSettingRepo{}, cfg)
	attempts := &excelBPSImageAdmissionAttempts{}
	gatewayService := service.NewOpenAIGatewayService(
		excelBPSImageAdmissionAccountRepo{excelBPSFailoverAccountRepo{openAIImagesFailoverAccountRepo{accounts: accounts}}, attempts},
		nil, nil, nil, nil, nil, nil, nil,
		cfg,
		nil, schedulingConcurrency, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, settingService, nil,
	)
	t.Cleanup(func() { _ = gatewayService.CloseExcelBPSImages() })
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		concurrencyService,
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil,
		cfg,
	)
	handler.maxAccountSwitches = 10
	return handler, attempts
}

// newExcelBPSImageAdmissionTestContext builds a client request as the gateway
// routes would mark it.
func newExcelBPSImageAdmissionTestContext(groupID int64) (*gin.Context, *httptest.ResponseRecorder) {
	c, rec := newExcelBPSFailoverTestContext(context.Background(), false)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI},
		User:    &service.User{ID: 100},
	})
	service.MarkExcelBPSImageAdmission(c)
	return c, rec
}

// holdExcelBPSImageSlot sends a BPS request whose upstream call blocks until
// the returned release runs, so it keeps the only image admission slot.
func holdExcelBPSImageSlot(t *testing.T, handler *OpenAIGatewayHandler, groupID int64, entered <-chan struct{}, release func()) {
	t.Helper()
	done := make(chan int, 1)
	go func() {
		c, rec := newExcelBPSImageAdmissionTestContext(groupID)
		handler.Responses(c)
		done <- rec.Code
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("holder request did not reach the BPS upstream")
	}
	t.Cleanup(func() {
		release()
		select {
		case code := <-done:
			require.Equal(t, http.StatusOK, code, "the holder must finish normally")
		case <-time.After(5 * time.Second):
			t.Fatal("holder request did not finish")
		}
	})
}

func blockingFirstBPSCall() (*excelBPSFailoverUpstream, <-chan struct{}, func()) {
	entered := make(chan struct{})
	blocked := make(chan struct{})
	upstream := &excelBPSFailoverUpstream{
		answer: func(int) *http.Response { return excelBPSCompleted() },
		onDo: func(call int) {
			if call == 0 {
				close(entered)
				<-blocked
			}
		},
	}
	return upstream, entered, sync.OnceFunc(func() { close(blocked) })
}

func TestOpenAIResponsesExcelBPSImageBusySwitchesToOtherAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DATA_DIR", t.TempDir())
	for _, loadBatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("load_batch=%t", loadBatch), func(t *testing.T) {
			upstream, entered, release := blockingFirstBPSCall()
			// The BPS account has the higher priority: the holder keeps its slot,
			// and the request under test tries it first.
			handler, attempts := newExcelBPSImageAdmissionTestHandler(t, upstream, loadBatch, []service.Account{
				excelBPSImageAdmissionAccount(2, 4242, true, 1),
				excelBPSImageAdmissionAccount(3, 4242, false, 2),
			})
			holdExcelBPSImageSlot(t, handler, 4242, entered, release)
			require.Equal(t, []int64{2}, upstream.calls())
			before := attempts.count()

			c, rec := newExcelBPSImageAdmissionTestContext(4242)
			handler.Responses(c)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "done")
			tried := attempts.since(before)
			require.NotEmpty(t, tried)
			require.Equal(t, int64(2), tried[0], "the BPS account is tried first")
			require.Equal(t, int64(3), tried[len(tried)-1], "then the request switches to the other account")
			require.Equal(t, []int64{2, 3}, upstream.calls(), "the busy BPS account must not reach the upstream again")
			require.Equal(t, basispoints.ResponsesURL, upstream.urls[0])
			require.NotEqual(t, basispoints.ResponsesURL, upstream.urls[1], "the other account keeps its own route")
		})
	}
}

func TestOpenAIResponsesExcelBPSImageBusyOnEveryAccountReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DATA_DIR", t.TempDir())
	for _, loadBatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("load_batch=%t", loadBatch), func(t *testing.T) {
			upstream, entered, release := blockingFirstBPSCall()
			handler, attempts := newExcelBPSImageAdmissionTestHandler(t, upstream, loadBatch, []service.Account{
				excelBPSImageAdmissionAccount(1, 3131, true, 1),
				excelBPSImageAdmissionAccount(2, 3131, true, 1),
			})
			holdExcelBPSImageSlot(t, handler, 3131, entered, release)
			before := attempts.count()

			c, rec := newExcelBPSImageAdmissionTestContext(3131)
			handler.Responses(c)

			require.ElementsMatch(t, []int64{1, 2}, attempts.since(before), "every account must be tried before giving up")

			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.Equal(t, "1", rec.Header().Get("Retry-After"))
			require.Equal(t, "server_error", gjson.Get(rec.Body.String(), "error.type").String())
			require.Equal(t, "basispoints_image_request_busy", gjson.Get(rec.Body.String(), "error.code").String())
			require.Equal(t, "Image relay request capacity is busy; retry later", gjson.Get(rec.Body.String(), "error.message").String())
			require.Len(t, upstream.calls(), 1, "rejected attempts must not reach the upstream")
		})
	}
}
