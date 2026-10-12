package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type excelBPSImageAdmissionSettingsRepo struct {
	SettingRepository
	values map[string]string
}

func (r *excelBPSImageAdmissionSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *excelBPSImageAdmissionSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func excelBPSImageAdmissionTestContext(marked bool) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if marked {
		MarkExcelBPSImageAdmission(c)
	}
	return c
}

func requireExcelBPSImageAdmissionFailover(t *testing.T, err error, reason GatewayFailureReason, status int, retryAfter string) {
	t.Helper()
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, reason, failover.Reason)
	require.Equal(t, status, failover.ClientStatusCode)
	require.True(t, failover.IsExcelBPSImageAdmissionRejection())
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.ShouldReportAccountScheduleFailure(), "a local rejection must not count against the account")
	require.Equal(t, retryAfter, failover.ResponseHeaders.Get("Retry-After"))
}

func TestExcelBPSImageAdmissionSlotsFollowBudgetAndRequestCap(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		bodyBytes              int64
		budgetMiB, maxRequests int
		want                   int
	}{
		{"default small", 1024, 0, 0, 128},
		{"default large", 32 << 20, 0, 0, 4},
		{"scaled small", 1024, 512, 128, 128},
		{"scaled large", 32 << 20, 512, 128, 4},
		{"larger configured budget", 32 << 20, 2048, 128, 8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var budget excelBPSImageAdmission
			weight := tt.bodyBytes
			if weight < excelBPSImageAdmissionMinBodyBytes {
				weight = excelBPSImageAdmissionMinBodyBytes
			}
			weight *= excelBPSImageAdmissionBodyMultiplier
			var releases []func()
			for len(releases) <= basispoints.MaxImageRequests {
				release, ok := budget.acquire(tt.budgetMiB, tt.maxRequests, weight)
				if !ok {
					break
				}
				releases = append(releases, release)
			}
			require.Len(t, releases, tt.want)
			for _, release := range releases {
				release()
			}
			require.Zero(t, budget.bytes)
			require.Zero(t, budget.requests)
		})
	}
}

func TestExcelBPSImageAdmissionReleaseIsIdempotent(t *testing.T) {
	var budget excelBPSImageAdmission
	release, ok := budget.acquire(0, 0, int64(DefaultExcelBPSImageBudgetMiB)<<20)
	require.True(t, ok)
	_, ok = budget.acquire(0, 0, 1)
	require.False(t, ok)
	release()
	release()
	require.Zero(t, budget.bytes)
	require.Zero(t, budget.requests)
}

func TestExcelBPSImageAdmissionLoweredLimitsWaitForExistingReservations(t *testing.T) {
	var budget excelBPSImageAdmission
	releases := make([]func(), 0, 512)
	for i := 0; i < 512; i++ {
		release, ok := budget.acquire(512, 512, 8<<20)
		require.True(t, ok, "slot %d must be usable", i)
		releases = append(releases, release)
	}
	_, ok := budget.acquire(512, 512, 8<<20)
	require.False(t, ok, "request cap must still reject excess requests")
	_, ok = budget.acquire(512, 32, 8<<20)
	require.False(t, ok, "lowering the cap must not admit requests until existing reservations drain")
	for _, release := range releases {
		release()
	}
	require.Zero(t, budget.bytes)
	require.Zero(t, budget.requests)
	release, ok := budget.acquire(512, 32, 256<<20)
	require.True(t, ok)
	_, ok = budget.acquire(512, 32, (256<<20)+1)
	require.False(t, ok, "lowered budget must apply after draining")
	release()
}

func TestExcelBPSImageAdmissionExpandedCeilings(t *testing.T) {
	var budget excelBPSImageAdmission
	releases := make([]func(), 0, basispoints.MaxImageRequests)
	for i := 0; i < basispoints.MaxImageRequests; i++ {
		release, ok := budget.acquire(basispoints.MaxImageBudgetMiB, basispoints.MaxImageRequests, 8<<20)
		require.True(t, ok, "request %d", i)
		releases = append(releases, release)
	}
	_, ok := budget.acquire(basispoints.MaxImageBudgetMiB, basispoints.MaxImageRequests, 8<<20)
	require.False(t, ok, "configured request cap must still reject overflow")
	for _, release := range releases {
		release()
	}
	// Exercise byte accounting above 32-bit range separately from the slot cap.
	whole, ok := budget.acquire(basispoints.MaxImageBudgetMiB, basispoints.MaxImageRequests, int64(basispoints.MaxImageBudgetMiB)<<20)
	require.True(t, ok)
	_, ok = budget.acquire(basispoints.MaxImageBudgetMiB, basispoints.MaxImageRequests, 1)
	require.False(t, ok)
	whole()
	require.Zero(t, budget.bytes)
	require.Zero(t, budget.requests)
}

func TestExcelBPSImageMaxBodyFollowsSmallestLimit(t *testing.T) {
	for _, tt := range []struct {
		name         string
		bodyLimitMiB int
		gatewayMax   int64
		want         int64
	}{
		{"configured image limit", 64, 256 << 20, 64 << 20},
		{"default image limit", 0, 256 << 20, int64(DefaultExcelBPSImageBodyLimitMiB) << 20},
		{"gateway limit is smaller", basispoints.MaxImageBodyMiB, 256 << 20, 256 << 20},
		{"image ceiling", basispoints.MaxImageBodyMiB + 1, 4 << 30, int64(basispoints.MaxImageBodyMiB) << 20},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxBodySize: tt.gatewayMax}}}
			require.Equal(t, tt.want, svc.excelBPSImageMaxBodyBytes(ExcelBPSImageRelaySettings{BodyLimitMiB: tt.bodyLimitMiB}))
		})
	}
}

func TestAdmitExcelBPSImageRequestSkipsInternalAndDisabledRequests(t *testing.T) {
	enabled := ExcelBPSImageRelaySettings{Enabled: true, BodyLimitMiB: 1, BudgetMiB: 512, MaxRequests: 1}
	for _, tt := range []struct {
		name     string
		marked   bool
		settings ExcelBPSImageRelaySettings
	}{
		{"internal request", false, enabled},
		{"image support disabled", true, ExcelBPSImageRelaySettings{BodyLimitMiB: 1, BudgetMiB: 512, MaxRequests: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			release, err := svc.admitExcelBPSImageRequest(excelBPSImageAdmissionTestContext(tt.marked), tt.settings, 2<<20)
			require.NoError(t, err)
			require.Zero(t, svc.excelBPSImageAdmission.requests)
			release()
		})
	}
}

func TestAdmitExcelBPSImageRequestRejectsForFailover(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 256 << 20}}}
	settings := ExcelBPSImageRelaySettings{Enabled: true, BodyLimitMiB: 1, BudgetMiB: 512, MaxRequests: 1}
	c := excelBPSImageAdmissionTestContext(true)

	_, err := svc.admitExcelBPSImageRequest(c, settings, (1<<20)+1)
	requireExcelBPSImageAdmissionFailover(t, err, ExcelBPSImageBodyTooLargeReason, http.StatusRequestEntityTooLarge, "")
	require.Zero(t, svc.excelBPSImageAdmission.requests, "a rejected body must not hold a slot")

	release, err := svc.admitExcelBPSImageRequest(c, settings, 1<<20)
	require.NoError(t, err)
	_, err = svc.admitExcelBPSImageRequest(c, settings, 16)
	requireExcelBPSImageAdmissionFailover(t, err, ExcelBPSImageRequestBusyReason, http.StatusServiceUnavailable, "1")
	release()

	release, err = svc.admitExcelBPSImageRequest(c, settings, 16)
	require.NoError(t, err, "a finished forward must release its slot")
	release()
	require.Zero(t, svc.excelBPSImageAdmission.requests)
}

// A full image budget only stops client requests that take the BPS route.
// The same request on another account, or an internal BPS check, still
// reaches the upstream.
func TestExcelBPSImageAdmissionOnlyAppliesToBPSForwards(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	body := []byte(`{"model":"gpt-6-astra","input":"hello","stream":true}`)
	other := excelAccount()
	other.ID = 301
	delete(other.Extra, "openai_excel_bps")
	for _, tt := range []struct {
		name     string
		account  *Account
		marked   bool
		wantBusy bool
	}{
		{"client request on BPS account", excelAccount(), true, true},
		{"client request on other account", other, true, false},
		{"internal BPS check", excelAccount(), false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(""))}}
			svc := openAIClientToolsTestService(upstream)
			svc.settingService = NewSettingService(&excelBPSImageAdmissionSettingsRepo{values: map[string]string{
				SettingKeyExcelBPSImageRelayEnabled: "true",
				SettingKeyExcelBPSImageBaseURL:      "https://images.example",
				SettingKeyExcelBPSImageMaxRequests:  "1",
			}}, svc.cfg)
			t.Cleanup(func() { _ = svc.CloseExcelBPSImages() })
			hold, ok := svc.excelBPSImageAdmission.acquire(DefaultExcelBPSImageBudgetMiB, 1, excelBPSImageAdmissionMinBodyBytes*excelBPSImageAdmissionBodyMultiplier)
			require.True(t, ok)
			defer hold()
			c := excelBPSImageAdmissionTestContext(tt.marked)

			_, err := svc.Forward(context.Background(), c, tt.account, body)

			if tt.wantBusy {
				requireExcelBPSImageAdmissionFailover(t, err, ExcelBPSImageRequestBusyReason, http.StatusServiceUnavailable, "1")
				require.Empty(t, upstream.requests, "a rejected BPS forward must not reach the upstream")
				require.False(t, c.Writer.Written(), "the handler must still be able to switch accounts")
				return
			}
			var failover *UpstreamFailoverError
			if errors.As(err, &failover) {
				require.False(t, failover.IsExcelBPSImageAdmissionRejection())
			}
			require.Len(t, upstream.requests, 1)
		})
	}
}
