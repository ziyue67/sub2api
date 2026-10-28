package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminUsageTimeRange(t *testing.T) {
	for _, tc := range []struct {
		name, start, end, tz, wantStart, wantEnd string
		wantDuration                             time.Duration
	}{
		{"midnight", "2026-09-30T00:00:00.123+09:00", "2026-10-01T00:00:00.123+09:00", "America/New_York", "2026-09-29T15:00:00.123Z", "2026-09-30T15:00:00.123Z", 24 * time.Hour},
		{"before_midnight", "2026-09-29T23:59:59.999+09:00", "2026-09-30T23:59:59.999+09:00", "UTC", "2026-09-29T14:59:59.999Z", "2026-09-30T14:59:59.999Z", 24 * time.Hour},
		{"dst_spring_rolling", "2026-03-07T02:30:00.123-05:00", "2026-03-08T03:30:00.123-04:00", "America/New_York", "2026-03-07T07:30:00.123Z", "2026-03-08T07:30:00.123Z", 24 * time.Hour},
		{"dst_fall_rolling", "2026-10-31T02:30:00.123-04:00", "2026-11-01T01:30:00.123-05:00", "America/New_York", "2026-10-31T06:30:00.123Z", "2026-11-01T06:30:00.123Z", 24 * time.Hour},
		{"calendar_two_days", "2026-09-30", "2026-10-01", "Asia/Seoul", "2026-09-29T15:00:00Z", "2026-10-01T15:00:00Z", 48 * time.Hour},
		{"calendar_dst_spring", "2026-03-08", "2026-03-08", "America/New_York", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", 23 * time.Hour},
		{"calendar_dst_fall", "2026-11-01", "2026-11-01", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z", 25 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"start_date": {tc.start}, "end_date": {tc.end}, "timezone": {tc.tz}, "nocache": {"1"}}.Encode()
			repo := &adminUsageRepoCapture{}
			router := newAdminUsageRequestTypeTestRouter(repo)
			for _, path := range []string{"/admin/usage", "/admin/usage/stats"} {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
				require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
				filters := repo.listFilters
				if path == "/admin/usage/stats" {
					filters = repo.statsFilters
				}
				require.NotNil(t, filters.StartTime)
				require.NotNil(t, filters.EndTime)
				require.Equal(t, tc.wantStart, filters.StartTime.UTC().Format(time.RFC3339Nano))
				require.Equal(t, tc.wantEnd, filters.EndTime.UTC().Format(time.RFC3339Nano))
				require.Equal(t, tc.wantDuration, filters.EndTime.Sub(*filters.StartTime))
			}
			// The same parser feeds snapshot, model, group, ranking and breakdown queries.
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/admin/dashboard/snapshot-v2?"+query, nil)
			start, end, err := parseTimeRange(c)
			require.NoError(t, err)
			require.Equal(t, tc.wantStart, start.UTC().Format(time.RFC3339Nano))
			require.Equal(t, tc.wantEnd, end.UTC().Format(time.RFC3339Nano))
			require.Equal(t, tc.wantDuration, end.Sub(start))
		})
	}
}

func TestAdminUsageTimeRangeRejectsInvalidBounds(t *testing.T) {
	for _, tc := range []struct{ start, end string }{
		{"2026-10-01T00:01:02", "2026-10-02T00:01:02Z"}, // A timestamp must specify its timezone.
		{"2026-10-01T00:01:02Z", "2026-10-02T00:01:02"},
		{"2026-10-02T00:01:02Z", "2026-10-01T00:01:02Z"},
		{"2026-10-01T00:01:02.123Z", "2026-10-01T00:01:02.123Z"},
		{"2026-10-02", "2026-09-30"},
	} {
		for _, path := range []string{"/admin/usage", "/admin/usage/stats"} {
			repo := &adminUsageRepoCapture{}
			router := newAdminUsageRequestTypeTestRouter(repo)
			query := url.Values{"start_date": {tc.start}, "end_date": {tc.end}, "nocache": {"1"}}.Encode()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
			require.Equal(t, http.StatusBadRequest, rec.Code, "%s?%s", path, query)
			require.Nil(t, repo.listFilters.StartTime)
			require.Nil(t, repo.statsFilters.StartTime)
		}
	}
}

func TestUsageStatsCacheKeyPreservesTimePrecision(t *testing.T) {
	start := time.Date(2026, 9, 30, 15, 1, 2, 123000000, time.UTC)
	end := start.Add(24 * time.Hour)
	filters := usagestats.UsageLogFilters{StartTime: &start, EndTime: &end}
	first := usageStatsCacheKey(filters)
	shiftedStart, shiftedEnd := start.Add(time.Millisecond), end.Add(time.Millisecond)
	filters.StartTime, filters.EndTime = &shiftedStart, &shiftedEnd
	require.NotEqual(t, first, usageStatsCacheKey(filters), "different millisecond boundaries must not reuse earlier totals")
	var key usageStatsCacheKeyData
	require.NoError(t, json.Unmarshal([]byte(first), &key))
	require.Equal(t, start.Format(time.RFC3339Nano), key.StartTime)
	require.Equal(t, end.Format(time.RFC3339Nano), key.EndTime)
}

type rollingDashboardRepo struct {
	service.UsageLogRepository
	windows map[string][][2]time.Time
}

func (r *rollingDashboardRepo) GetUsageTrendWithUsageFilters(_ context.Context, start, end time.Time, _ string, _ usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error) {
	r.windows["trend"] = append(r.windows["trend"], [2]time.Time{start, end})
	return []usagestats.TrendDataPoint{}, nil
}

func (r *rollingDashboardRepo) GetModelStatsWithUsageFiltersBySource(_ context.Context, start, end time.Time, _ usagestats.UsageLogFilters, _ string) ([]usagestats.ModelStat, error) {
	r.windows["models"] = append(r.windows["models"], [2]time.Time{start, end})
	return []usagestats.ModelStat{}, nil
}

func (r *rollingDashboardRepo) GetGroupStatsWithUsageFilters(_ context.Context, start, end time.Time, _ usagestats.UsageLogFilters) ([]usagestats.GroupStat, error) {
	r.windows["groups"] = append(r.windows["groups"], [2]time.Time{start, end})
	return []usagestats.GroupStat{}, nil
}

func TestAdminDashboardRolling24HoursQueriesAndCachePrecision(t *testing.T) {
	resetDashboardReadCachesForTest()
	t.Cleanup(resetDashboardReadCachesForTest)
	repo := &rollingDashboardRepo{windows: make(map[string][][2]time.Time)}
	handler := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
	router := gin.New()
	router.GET("/snapshot", handler.GetSnapshotV2)
	router.GET("/models", handler.GetModelStats)
	end, err := time.Parse(time.RFC3339Nano, "2026-10-01T00:00:00.123+09:00")
	require.NoError(t, err)
	for _, shift := range []time.Duration{0, 0, time.Millisecond} {
		upper := end.Add(shift)
		query := url.Values{
			"start_date":    {upper.Add(-24 * time.Hour).Format(time.RFC3339Nano)},
			"end_date":      {upper.Format(time.RFC3339Nano)},
			"timezone":      {"America/New_York"},
			"include_stats": {"false"}, "include_trend": {"true"},
			"include_model_stats": {"true"}, "include_group_stats": {"true"},
		}.Encode()
		for _, path := range []string{"/snapshot", "/models"} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}
	}
	for _, name := range []string{"trend", "models", "groups"} {
		require.Len(t, repo.windows[name], 2, "%s must cache identical bounds but distinguish milliseconds", name)
		for i, window := range repo.windows[name] {
			wantEnd := end.Add(time.Duration(i) * time.Millisecond)
			require.True(t, window[0].Equal(wantEnd.Add(-24*time.Hour)), name)
			require.True(t, window[1].Equal(wantEnd), name)
			require.Equal(t, 24*time.Hour, window[1].Sub(window[0]), name)
		}
	}
}

func TestAdminDashboardRejectsInvalidTimeRange(t *testing.T) {
	handler := NewDashboardHandler(nil, nil)
	router := gin.New()
	router.GET("/snapshot", handler.GetSnapshotV2)
	router.GET("/models", handler.GetModelStats)
	for _, path := range []string{"/snapshot", "/models"} {
		for _, bounds := range [][2]string{
			{"2026-10-01T00:01:02", "2026-10-02T00:01:02Z"},
			{"2026-10-02T00:01:02Z", "2026-10-01T00:01:02Z"},
		} {
			query := url.Values{"start_date": {bounds[0]}, "end_date": {bounds[1]}}.Encode()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		}
	}
}
