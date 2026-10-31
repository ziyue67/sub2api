//go:build unit

package service

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIssue605NativeLoopbackSSETypedContentAndByte(t *testing.T) {
	wire := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic605\",\"created_at\":1}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\"}}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"summary\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic605\",\"created_at\":1,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, wire)
	}))
	defer server.Close()
	collector := requesttiming.New(time.Now(), 0)
	ctx := requesttiming.With(context.Background(), collector)
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader("synthetic"))
	require.NoError(t, err)
	req, _ = requesttiming.StartAttempt(req, 605, 0)
	req, tr := requesttiming.StartTransport(req)
	resp, err := server.Client().Do(req)
	tr.Response(resp, err)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	_, err = svc.handleStreamingResponse(req.Context(), resp, c, &Account{ID: 605, Platform: PlatformOpenAI}, time.Now(), "fixture", "fixture")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	collector.Finish(200, false)
	collector.WhenFinished(func(s requesttiming.Snapshot) {
		v := requesttiming.DeriveContentObservation(s)
		require.NotNil(t, v.ResponseByteFirstMS)
		require.NotNil(t, v.ReasoningFirstMS, "native mixed SSE must record the typed reasoning item")
		require.NotNil(t, v.VisibleTextFirstMS)
		require.LessOrEqual(t, *v.ResponseByteFirstMS, *v.ReasoningFirstMS)
		require.LessOrEqual(t, *v.ReasoningFirstMS, *v.VisibleTextFirstMS)
		require.Equal(t, "unknown", v.ReasoningStreamingCapability)
		require.Contains(t, c.Writer.Header().Get("Content-Type"), "text/event-stream")
	})
}

func TestIssue605MixedSSERecordsTypedBoundaries(t *testing.T) {
	preamble := "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\"}}\n\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\"}}\n\n"
	for _, tc := range []struct {
		wire         string
		reason, text bool
	}{
		{preamble, false, false},
		{preamble + "event: response.output_item.added\ndata: {\"item\":{\"type\":\"reasoning\"}}\n\n", true, false},
		{preamble + "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"synthetic\"}\n\n", true, false},
		{preamble + "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"synthetic\"}\n\n", true, true},
	} {
		collector := requesttiming.New(time.Now(), 0)
		req := httptest.NewRequest("POST", "/v1/responses", nil).WithContext(requesttiming.With(context.Background(), collector))
		req, _ = requesttiming.StartAttempt(req, 1, 0)
		req, _ = requesttiming.StartTransport(req)
		scanner := newOpenAISSEJSONDocumentScanner(bufio.NewScanner(strings.NewReader(tc.wire)))
		scanner.ctx = req.Context()
		for scanner.Scan() {
		}
		require.NoError(t, scanner.Err())
		collector.Finish(200, false)
		collector.WhenFinished(func(s requesttiming.Snapshot) {
			_, reason := s.Attempts[1].Events["first_reasoning"]
			_, text := s.Attempts[1].Events["first_visible_text"]
			require.Equal(t, tc.reason, reason, "typed mixed SSE must observe reasoning without classifying message/function_call as reasoning")
			require.Equal(t, tc.text, text, "visible text must remain distinct from reasoning summary")
		})
	}
}

func TestIssue605DefaultAndAdvancedFeedbackEffortIsolation(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	now := time.Now()
	old, recent := now.Add(-time.Hour), now.Add(-time.Minute)
	accounts := []Account{{ID: 6051, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 10, LastUsedAt: &old}, {ID: 6052, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 10, LastUsedAt: &recent}}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}), openaiAccountStats: newOpenAIAccountRuntimeStats()}
	key := priorityAttemptKey{accountID: 6051, model: "fixture", endpoint: "/v1/responses", effort: "xhigh"}
	for range 5 {
		svc.openaiAccountStats.attempts.report(key, false, now)
	}
	for _, tc := range []struct {
		effort string
		want   int64
	}{{"xhigh", 6052}, {"medium", 6051}, {"max", 6051}, {"", 6051}} {
		ctx := WithObservationEffort(WithPrioritySchedulingObservation(context.Background(), nil, "fixture", "/v1/responses"), tc.effort)
		selection, _, err := svc.SelectAccountWithScheduler(ctx, nil, "", "", "fixture", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.Equal(t, tc.want, selection.Account.ID, "default feedback must isolate effort populations")
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	}
	for _, effort := range []string{"medium", "max", ""} {
		ctx := WithObservationEffort(context.Background(), effort)
		require.Zero(t, svc.defaultFeedbackPenalty(ctx, nil, "fixture", 6051), "xhigh must not contaminate another effort")
	}
	svc.openaiAccountStats.report(6051, false, nil)
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: svc.openaiAccountStats}
	plan := scheduler.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{RequestedModel: "fixture", Platform: PlatformOpenAI, ObservationEffort: "medium", ObservationEffortScoped: true}, []*Account{&accounts[0], &accounts[1]}, map[int64]*AccountLoadInfo{})
	for _, candidate := range plan.allCandidates {
		require.Zero(t, candidate.errorRate, "advanced scoped plan must not reuse account-wide error EWMA")
	}
	request := OpenAIAccountScheduleRequest{RequestedModel: "fixture", ObservationEffort: "xhigh", ObservationEffortScoped: true}
	c := DefaultPrioritySchedulingConfig()
	a := priorityHistoryKey(request, c)
	request.ObservationEffort = "medium"
	require.NotEqual(t, a, priorityHistoryKey(request, c), "priority history cache must isolate effort")
}

func qdiscFixture(drops uint32, parent uint32) []byte {
	queue := make([]byte, 24)
	binary.NativeEndian.PutUint16(queue[:2], 24)
	binary.NativeEndian.PutUint16(queue[2:4], 3)
	binary.NativeEndian.PutUint32(queue[12:16], drops)
	nested := make([]byte, 28)
	binary.NativeEndian.PutUint16(nested[:2], 28)
	binary.NativeEndian.PutUint16(nested[2:4], 7)
	copy(nested[4:], queue)
	message := make([]byte, 48)
	binary.NativeEndian.PutUint32(message[4:8], 2)
	binary.NativeEndian.PutUint32(message[8:12], 65536)
	binary.NativeEndian.PutUint32(message[12:16], parent)
	copy(message[20:], nested)
	return message
}
func TestIssue605QdiscRootFixturesAndCollectorDelta(t *testing.T) {
	root := qdiscFixture(10, 0xffffffff)
	child := qdiscFixture(900, 65536)
	value, err := parseQdiscMessages([][]byte{root, child})
	require.NoError(t, err)
	require.Len(t, value.drops, 1, "nested qdiscs cannot double count parent statistics")
	_, err = parseQdiscMessages([][]byte{root[:24]})
	require.Error(t, err)
	for key := range value.drops {
		require.EqualValues(t, 10, value.drops[key])
	}
	current := uint32(10)
	sampler := &hostNetworkSampler{readFile: func(string) ([]byte, error) { return []byte(""), nil }, readQdisc: func() qdiscSnapshot {
		v, e := parseQdiscMessages([][]byte{qdiscFixture(current, 0xffffffff)})
		require.NoError(t, e)
		return v
	}}
	now := time.Now()
	first := sampler.sample(now, "fixture")
	require.Nil(t, first.QdiscDrops)
	current = 13
	next := sampler.sample(now.Add(time.Minute), "fixture")
	require.Equal(t, "measured_root_egress_qdisc", next.QdiscStatus)
	require.EqualValues(t, 3, *next.QdiscDrops)
	store := &issue603NetworkStore{}
	collector := &OpsMetricsCollector{opsRepo: store, networkSampler: sampler, instanceID: "fixture"}
	current = 17
	require.NoError(t, collector.collectNetwork(context.Background(), now.Add(2*time.Minute)))
	require.Len(t, store.points, 1)
	require.EqualValues(t, 4, *store.points[0].QdiscDrops, "parsed qdisc delta must reach the existing collector persistence adapter")
	current = 1
	next = sampler.sample(now.Add(3*time.Minute), "fixture")
	require.Nil(t, next.QdiscDrops)
	require.Equal(t, "counter_reset_or_topology_change", next.QdiscStatus)
	sampler.readQdisc = func() qdiscSnapshot { return qdiscSnapshot{status: "permission_denied"} }
	next = sampler.sample(now.Add(4*time.Minute), "fixture")
	require.Equal(t, "permission_denied", next.QdiscStatus)
	require.Nil(t, next.QdiscDrops)
}
