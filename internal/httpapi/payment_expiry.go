package httpapi

import (
	"context"
	"log"
	"time"
)

const stalePaymentTimerKey = "stale_payment_failed_after"

// RunPaymentExpiry runs on every API replica. The repository uses row locks
// with SKIP LOCKED, so replicas may sweep concurrently without double
// emitting a terminal payment event.
func (a *API) RunPaymentExpiry(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	sweep := func() {
		timer, err := a.timers.Get(ctx, stalePaymentTimerKey)
		if err != nil {
			log.Printf("payment timeout timer unavailable: %v", err)
			return
		}
		if !timer.Enabled {
			return
		}
		failed, err := a.payments.FailStale(ctx, time.Duration(timer.DurationSeconds)*time.Second)
		if err != nil {
			log.Printf("payment timeout sweep failed: %v", err)
			return
		}
		for _, payment := range failed {
			a.emitPaymentMessageAsync(payment, "payment.updated")
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
