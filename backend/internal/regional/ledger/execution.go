package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
)

type RequestLedger interface {
	Reserve(context.Context, Reservation) (bool, error)
	Start(context.Context, string) error
	CancelBeforeStart(context.Context, string) error
	Complete(context.Context, Event) error
}

// MeasuredUsage must come from a terminal provider usage event, not inferred
// from EOF, HTTP status, streamed text length or a client-reported amount.
type MeasuredUsage struct {
	Input, Output, CacheCreation, CacheRead int64
	Final                                   bool
}

// ExecuteBounded is the dispatch boundary for a metered text adapter. send may
// write HTTP/SSE/WS bytes only after Start has committed. After Start, all errors
// (including cancellation and missing usage) retain the maximum indefinitely.
// No internal retry is allowed. Each WS turn requires its own immutable ID.
// The caller still must validate supported request features, authenticate and
// reserve shared concurrency/RPM before invoking this boundary.
func ExecuteBounded(ctx context.Context, l RequestLedger, p Price, r Reservation, payload []byte, maxOutput int64, send func(context.Context) (MeasuredUsage, error)) error {
	if l == nil || send == nil {
		return errors.New("metered dispatch dependencies required")
	}
	maximum, err := p.Maximum(maxOutput)
	if err != nil {
		return err
	}
	if maximum <= 0 {
		return errors.New("nonzero conservative reservation required")
	}
	digest := sha256.Sum256(payload)
	r.Maximum = maximum
	r.Model = p.Model
	r.PricingVersion = p.Version()
	r.PayloadSHA256 = hex.EncodeToString(digest[:])
	fresh, err := l.Reserve(ctx, r)
	if err != nil {
		return err
	}
	if !fresh {
		return ErrAlreadyDispatched
	}
	if err = ctx.Err(); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return errors.Join(err, l.CancelBeforeStart(cleanup, r.RequestID))
	}
	// An ambiguous Start failure is never followed by Cancel: the commit may
	// have succeeded even if its acknowledgement was lost.
	if err = l.Start(ctx, r.RequestID); err != nil {
		return err
	}
	usage, sendErr := send(ctx)
	if sendErr != nil && !usage.Final {
		return sendErr
	}
	if !usage.Final {
		return errors.New("terminal usage missing; reservation retained")
	}
	if usage.Output > maxOutput {
		return errors.New("output exceeds enforced request limit; reservation retained")
	}
	charge, err := p.Charge(usage.Input, usage.Output, usage.CacheCreation, usage.CacheRead)
	if err != nil {
		return err
	}
	e := Event{Usage: regional.UsageEvent{SchemaVersion: regional.SchemaVersion, EventID: r.RequestID, LeaseID: r.LeaseID, RegionID: r.RegionID, UserID: r.UserID, APIKeyID: r.APIKeyID, AccountID: r.AccountID, AdmittedAt: r.AdmittedAt, CompletedAt: time.Now().UTC(), Charge: charge}, Model: r.Model, PricingVersion: r.PricingVersion, PayloadSHA256: r.PayloadSHA256, InputTokens: usage.Input, OutputTokens: usage.Output, CacheCreationTokens: usage.CacheCreation, CacheReadTokens: usage.CacheRead}
	// Client disconnect after a complete terminal usage event must not discard
	// settlement. A database failure leaves the persisted started hold intact.
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return errors.Join(sendErr, l.Complete(settle, e))
}
