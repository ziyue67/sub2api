package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// QueueTiming keeps explicitly sampled server and forwarding observations apart
// from protocol-specific legacy duration/first_token fields. It is never client E2E.
type QueueTiming struct {
	Status              string `json:"status"`
	Source              string `json:"source"`
	Scope               string `json:"scope"`
	ServerFirstTokenMs  *int64 `json:"server_first_token_ms"`
	ServerDurationMs    int64  `json:"server_duration_ms"`
	ForwardFirstTokenMs *int64 `json:"forward_first_token_ms"`
	ForwardDurationMs   int64  `json:"forward_duration_ms"`
	QueueClamped        bool   `json:"queue_clamped"`
}

func openAIIngressStart(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	start, _ := ctx.Value(ctxkey.RequestStartTime).(time.Time)
	return start
}

// L is the legacy timer's sampled baseline, not reconstructed from callback time.
// F is frozen at the first send for a bridge turn, or the final-attempt baseline
// for ctx_pool/HTTP. Failed earlier attempts in the latter scope are elapsed
// pre-final-forward time; source/scope disclose that distinction.
func applyOpenAIQueueTiming(r *OpenAIForwardResult, arrival, forward, legacy time.Time, source, scope string) {
	if r == nil {
		return
	}
	r.QueueWait, r.QueueWaitMeasured, r.QueueTiming = 0, false, nil
	if r.OpenAIWSMode && r.UpstreamTerminalEvent == "error" && r.RequestID == "" {
		return
	}
	if arrival.IsZero() || forward.IsZero() || legacy.IsZero() || forward.Before(arrival) || legacy.Before(arrival) || forward.Before(legacy) || r.Duration < 0 {
		return
	}
	queue := forward.Sub(arrival)
	offset := legacy.Sub(arrival)
	overlap := forward.Sub(legacy)
	if overlap > r.Duration {
		return
	}
	q := queue
	if q > maxOpenAIQueueWait {
		q = maxOpenAIQueueWait
	}
	r.QueueWait, r.QueueWaitMeasured = q, true
	t := &QueueTiming{Status: "measured", Source: source, Scope: scope, ServerDurationMs: r.Duration.Milliseconds() + offset.Milliseconds(), ForwardDurationMs: r.Duration.Milliseconds() - overlap.Milliseconds(), QueueClamped: q != queue}
	if r.FirstTokenMs != nil && *r.FirstTokenMs >= 0 && int64(*r.FirstTokenMs) >= overlap.Milliseconds() {
		server := int64(*r.FirstTokenMs) + offset.Milliseconds()
		fwd := int64(*r.FirstTokenMs) - overlap.Milliseconds()
		t.ServerFirstTokenMs, t.ForwardFirstTokenMs = &server, &fwd
	}
	r.QueueTiming = t
}

type openAIWSQueueArrivalKey struct{}

func openAIWSQueueArrival(ctx context.Context) time.Time {
	t, _ := ctx.Value(openAIWSQueueArrivalKey{}).(time.Time)
	return t
}
