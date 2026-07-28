// Package clock provides a swappable wall-clock seam so other packages can record real
// timestamps in production while pinning a deterministic time in tests, without any mutable
// package-level state.
package clock

import (
	"context"
	"time"
)

type nowContextKey struct{}

// Now returns the time pinned in ctx via SetNowToContext, or the real current time otherwise.
func Now(ctx context.Context) time.Time {
	if now, ok := nowFromContext(ctx); ok {
		return now
	}
	return time.Now()
}

// SetNowToContext returns a copy of ctx that pins now as the clock's value for that context.
func SetNowToContext(ctx context.Context, now time.Time) context.Context {
	return context.WithValue(ctx, nowContextKey{}, now)
}

func nowFromContext(ctx context.Context) (time.Time, bool) {
	now, ok := ctx.Value(nowContextKey{}).(time.Time)
	return now, ok
}
