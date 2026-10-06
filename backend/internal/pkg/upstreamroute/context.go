package upstreamroute

import "context"

type accountIDContextKey struct{}

func WithAccountID(ctx context.Context, accountID int64) context.Context {
	if accountID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, accountIDContextKey{}, accountID)
}

func AccountIDFromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(accountIDContextKey{}).(int64)
	return id
}
