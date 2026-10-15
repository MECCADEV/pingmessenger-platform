package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLocalLimiterUsesWindowedKeys(t *testing.T) {
	l := NewLocal()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if ok, err := l.Allow(ctx, "user:a", 2, time.Minute); err != nil || !ok {
			t.Fatalf("allow %d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, err := l.Allow(ctx, "user:a", 2, time.Minute); err != nil || ok {
		t.Fatalf("expected third request to be limited: ok=%v err=%v", ok, err)
	}
	if ok, err := l.Allow(ctx, "ip:1.2.3.4", 1, time.Minute); err != nil || !ok {
		t.Fatalf("independent key was limited: ok=%v err=%v", ok, err)
	}
}
