//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type initialAdmissionRepo struct {
	excelBPSFailoverAccountRepo
	changed      map[int64]bool
	readErr      error
	onRead       func()
	initialReads []int64
	slots        *helperConcurrencyCacheStub
}

func (r *initialAdmissionRepo) GetOpenAITurnAdmission(ctx context.Context, id int64) (*service.Account, *service.Account, error) {
	r.initialReads = append(r.initialReads, id)
	if r.onRead != nil {
		r.onRead()
	}
	if r.readErr != nil {
		return nil, nil, r.readErr
	}
	a, err := r.GetByID(ctx, id)
	if a != nil && r.changed[id] {
		a.Extra = maps.Clone(a.Extra)
		a.Extra["openai_excel_bps"] = false
	}
	return a, nil, err
}

func newInitialAdmissionHandler(t *testing.T, repo *initialAdmissionRepo, upstream service.HTTPUpstream) *OpenAIGatewayHandler {
	t.Helper()
	for id := int64(1); id <= 2; id++ {
		repo.accounts = append(repo.accounts, service.Account{
			ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{3131}, Priority: int(id), Concurrency: 1,
			Credentials: map[string]any{"access_token": fmt.Sprintf("test-token-%d", id), "chatgpt_account_id": fmt.Sprintf("test-account-%d", id)},
			Extra:       map[string]any{"openai_excel_bps": true},
		})
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	repo.slots = &helperConcurrencyCacheStub{accountSeq: []bool{true, true, true}}
	concurrency := service.NewConcurrencyService(repo.slots)
	gw := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, nil, cfg,
		nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := NewOpenAIGatewayHandler(gw, concurrency, billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.maxAccountSwitches = 2
	return h
}

func TestOpenAIInitialAdmissionReselectsBeforeSending(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("chat=%t/stream=%t", chat, stream), func(t *testing.T) {
				repo := &initialAdmissionRepo{changed: map[int64]bool{1: true}}
				upstream := &excelBPSFailoverUpstream{answer: func(int) *http.Response {
					resp := excelBPSCompleted()
					wire, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"done\",\"output_index\":0,\"content_index\":0}\n\n"
					resp.Body = io.NopCloser(strings.NewReader(delta + string(wire)))
					return resp
				}}
				h := newInitialAdmissionHandler(t, repo, upstream)
				c, rec := newExcelBPSFailoverTestContext(context.Background(), stream)
				if chat {
					// Exercise the same Chat Completions messages shape as model tests.
					body := fmt.Sprintf(`{"model":"gpt-6-astra","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream)
					c.Request.Body = io.NopCloser(strings.NewReader(body))
					c.Request.ContentLength = int64(len(body))
					c.Request.URL.Path = "/v1/chat/completions"
					h.ChatCompletions(c)
				} else {
					h.Responses(c)
				}
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "done")
				require.Equal(t, []int64{2}, upstream.calls(), "the stale route must never be sent")
				require.Equal(t, int64(1), repo.initialReads[0])
				repo.slots.mu.Lock()
				acquired, released := repo.slots.accountAcquireCalls, repo.slots.accountReleaseCalls
				repo.slots.mu.Unlock()
				require.Equal(t, 2, acquired)
				require.Equal(t, acquired, released, "both rejected and successful attempts release their slots")
				require.NotContains(t, rec.Body.String(), "admission_unavailable")
				_, recordedUpstreamError := c.Get(service.OpsUpstreamErrorsKey)
				require.False(t, recordedUpstreamError, "a local route change is not an upstream failure")
			})
		}
	}
}

func TestOpenAIInitialAdmissionRetryBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, chat := range []bool{false, true} {
		for _, kind := range []string{"budget", "all_changed", "database_error", "cancel", "downstream_written", "conversation"} {
			t.Run(fmt.Sprintf("chat=%t/%s", chat, kind), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				repo := &initialAdmissionRepo{changed: map[int64]bool{1: true}}
				upstream := &excelBPSFailoverUpstream{answer: func(int) *http.Response { return excelBPSCompleted() }}
				h := newInitialAdmissionHandler(t, repo, upstream)
				c, rec := newExcelBPSFailoverTestContext(ctx, false)
				switch kind {
				case "budget":
					h.maxAccountSwitches = 0
				case "all_changed":
					repo.changed[2] = true
				case "database_error":
					repo.readErr = errors.New("test database unavailable")
				case "cancel":
					repo.onRead = cancel
				case "downstream_written":
					repo.onRead = func() { c.Writer.WriteHeaderNow() }
				case "conversation":
					c.Request.Body = http.NoBody
					body := `{"model":"gpt-6-astra","input":"hello","conversation":"conv_test"}`
					c.Request.Body = io.NopCloser(strings.NewReader(body))
					c.Request.ContentLength = int64(len(body))
				}
				if chat {
					c.Request.URL.Path = "/v1/chat/completions"
					h.ChatCompletions(c)
				} else {
					h.Responses(c)
				}
				require.Empty(t, upstream.calls(), "unsafe/exhausted admission must not send")
				if kind == "all_changed" {
					require.Equal(t, []int64{1, 2}, repo.initialReads)
				} else {
					require.Equal(t, []int64{1}, repo.initialReads)
				}
				if kind != "cancel" && kind != "downstream_written" {
					require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
					require.Contains(t, rec.Body.String(), "admission_unavailable")
				}
			})
		}
	}
}

func TestOpenAIInitialAdmissionCannotReplayContinuationOrOutput(t *testing.T) {
	for _, kind := range []string{"previous_response_id", "conversation", "partial_result", "written", "canceled", "budget", "already_excluded", "later_send"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repo := &initialAdmissionRepo{changed: map[int64]bool{1: true}}
			upstream := &excelBPSFailoverUpstream{answer: func(int) *http.Response { t.Fatal("unexpected upstream call"); return nil }}
			h := newInitialAdmissionHandler(t, repo, upstream)
			c, _ := newExcelBPSFailoverTestContext(ctx, false)
			account, err := repo.GetByID(ctx, 1)
			require.NoError(t, err)
			_, admissionErr := h.gatewayService.Forward(ctx, c, account, []byte(`{"model":"gpt-6-astra"}`))
			require.True(t, service.IsOpenAIInitialAdmissionRejection(admissionErr))
			body := []byte(`{"model":"gpt-6-astra","input":"hello"}`)
			excluded := map[int64]struct{}{}
			switches, budget := 0, 2
			var result *service.OpenAIForwardResult
			switch kind {
			case "previous_response_id":
				body = []byte(`{"previous_response_id":"resp_test"}`)
			case "conversation":
				body = []byte(`{"conversation":{"id":"conv_test"}}`)
			case "partial_result":
				result = &service.OpenAIForwardResult{}
			case "written":
				_, err = c.Writer.Write([]byte(": heartbeat\n\n"))
				require.NoError(t, err)
			case "canceled":
				cancel()
			case "budget":
				budget = 0
			case "already_excluded":
				excluded[1] = struct{}{}
			case "later_send":
				admissionErr = &service.OpenAITurnAdmissionError{Reason: "account_binding_changed"}
			}
			require.False(t, retryOpenAIInitialAdmission(c, admissionErr, result, body, 1, excluded, &switches, budget))
			require.Zero(t, switches)
			require.Empty(t, upstream.calls())
		})
	}
}
