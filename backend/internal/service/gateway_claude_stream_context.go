package service

import "context"

// newClaudeStreamUpstreamContext propagates cancellation while waiting for an
// accepted response (including retries). Once accepted, the stream reader owns
// the bounded drain and closes the body when final Usage arrives or time expires.
// Non-streaming requests retain their original cancellation and deadline.
func newClaudeStreamUpstreamContext(ctx context.Context, stream bool) (upstream context.Context, accepted func(), release context.CancelFunc) {
	if !stream {
		return ctx, func() {}, func() {}
	}
	upstream, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopCancellation := context.AfterFunc(ctx, cancel)
	if ctx.Err() != nil {
		cancel()
	}
	return upstream, func() { stopCancellation() }, func() {
		stopCancellation()
		cancel()
	}
}
