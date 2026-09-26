package service

import (
	"context"
	"slices"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type observerScopeKey struct{}

var ErrObserverScope = infraerrors.Forbidden("OBSERVER_SCOPE_FORBIDDEN", "Account or group is outside the observer's assigned groups")

// WithObserverScope carries the freshly loaded user's management grants. An
// empty, present scope grants nothing; absence means this is not an observer.
// These grants are independent of the user's API consumption allowed_groups.
func WithObserverScope(ctx context.Context, groupIDs []int64) context.Context {
	return context.WithValue(ctx, observerScopeKey{}, slices.Clone(groupIDs))
}

func ObserverGroupIDs(ctx context.Context) ([]int64, bool) {
	ids, ok := ctx.Value(observerScopeKey{}).([]int64)
	return slices.Clone(ids), ok
}

func ObserverCanManageGroup(ctx context.Context, id int64) bool {
	ids, scoped := ObserverGroupIDs(ctx)
	return !scoped || id > 0 && slices.Contains(ids, id)
}

func ObserverCanManageAccount(ctx context.Context, account *Account) bool {
	_, scoped := ObserverGroupIDs(ctx)
	if !scoped {
		return true
	}
	if account != nil {
		for _, id := range account.GroupIDs {
			if ObserverCanManageGroup(ctx, id) {
				return true
			}
		}
	}
	return false
}

func ValidateObserverGroupBindings(ctx context.Context, ids []int64) error {
	if _, scoped := ObserverGroupIDs(ctx); !scoped {
		return nil
	}
	if len(ids) == 0 {
		return ErrObserverScope
	}
	for _, id := range ids {
		if !ObserverCanManageGroup(ctx, id) {
			return ErrObserverScope
		}
	}
	return nil
}

// ObserverVisibleGroups restricts selectors and new copies to assigned groups.
func ObserverVisibleGroups(ctx context.Context, ids []int64) []int64 {
	return slices.DeleteFunc(slices.Clone(ids), func(id int64) bool { return !ObserverCanManageGroup(ctx, id) })
}
