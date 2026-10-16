package ledger

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

type executionLedger struct {
	calls    []string
	fresh    bool
	startErr error
	reserve  func()
	event    Event
	canceled bool
}

func (l *executionLedger) Reserve(_ context.Context, _ Reservation) (bool, error) {
	l.calls = append(l.calls, "reserve")
	if l.reserve != nil {
		l.reserve()
	}
	return l.fresh, nil
}
func (l *executionLedger) Start(_ context.Context, _ string) error {
	l.calls = append(l.calls, "start")
	return l.startErr
}
func (l *executionLedger) CancelBeforeStart(ctx context.Context, _ string) error {
	l.calls = append(l.calls, "cancel")
	l.canceled = ctx.Err() != nil
	return nil
}
func (l *executionLedger) Complete(ctx context.Context, e Event) error {
	l.calls = append(l.calls, "complete")
	l.event = e
	l.canceled = ctx.Err() != nil
	return nil
}
func TestExecuteBoundedLifecycle(t *testing.T) {
	cases := []struct {
		name              string
		usage             MeasuredUsage
		sendErr, startErr error
		fresh, complete   bool
	}{
		{name: "success", usage: MeasuredUsage{Input: 10, Output: 2, Final: true}, fresh: true, complete: true},
		{name: "EOF without usage", fresh: true},
		{name: "cancel without usage", sendErr: context.Canceled, fresh: true},
		{name: "disconnect after terminal", usage: MeasuredUsage{Input: 10, Output: 2, Final: true}, sendErr: context.Canceled, fresh: true, complete: true},
		{name: "output exceeds requested bound", usage: MeasuredUsage{Input: 10, Output: 11, Final: true}, fresh: true},
		{name: "input exceeds model bound", usage: MeasuredUsage{Input: 1001, Output: 2, Final: true}, fresh: true},
		{name: "ambiguous Start commit", startErr: errors.New("ack lost"), fresh: true},
		{name: "permanent retry", fresh: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &executionLedger{fresh: tc.fresh, startErr: tc.startErr}
			err := ExecuteBounded(context.Background(), l, textPrice(), Reservation{RequestID: "request"}, []byte("payload"), 10, func(context.Context) (MeasuredUsage, error) {
				require.Equal(t, []string{"reserve", "start"}, l.calls)
				l.calls = append(l.calls, "send")
				return tc.usage, tc.sendErr
			})
			if tc.name == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, tc.complete, l.event.Model != "")
			require.NotContains(t, l.calls, "cancel")
		})
	}
}
func TestExecuteBoundedCancellationAfterReservation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &executionLedger{fresh: true, reserve: cancel}
	err := ExecuteBounded(ctx, l, textPrice(), Reservation{}, nil, 10, func(context.Context) (MeasuredUsage, error) { t.Fatal("must not send"); return MeasuredUsage{}, nil })
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{"reserve", "cancel"}, l.calls)
	require.False(t, l.canceled)
}
func TestExecuteBoundedSettlesAfterClientCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &executionLedger{fresh: true}
	require.NoError(t, ExecuteBounded(ctx, l, textPrice(), Reservation{}, nil, 10, func(context.Context) (MeasuredUsage, error) {
		cancel()
		return MeasuredUsage{Input: 1, Output: 1, Final: true}, nil
	}))
	require.False(t, l.canceled)
	require.Equal(t, "mock-text", l.event.Model)
}
