package httpapi

import (
	"context"
	"testing"
	"time"
)

type recordingLimiter struct {
	keys []string
}

func (l *recordingLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, error) {
	l.keys = append(l.keys, key)
	return true, nil
}

func TestAllowRateChecksPeerBeforeUser(t *testing.T) {
	limiter := &recordingLimiter{}
	a := &API{limiter: limiter}
	allowed, err := a.allowRate(context.Background(), "document-download", "user-1", "203.0.113.10", 10, 20)
	if err != nil || !allowed {
		t.Fatalf("allowRate() allowed=%v err=%v", allowed, err)
	}
	if len(limiter.keys) != 2 || limiter.keys[0] != "pm:rate:document-download:ip:203.0.113.10" || limiter.keys[1] != "pm:rate:document-download:user:user-1" {
		t.Fatalf("unexpected limiter order: %#v", limiter.keys)
	}
}
