package payments

import (
	"testing"
	"time"
)

func TestPublishOnlyFansOutToPaymentParticipants(t *testing.T) {
	service := NewService(nil)
	from, unsubscribeFrom := service.Subscribe("conversation-1", "user-from")
	defer unsubscribeFrom()
	to, unsubscribeTo := service.Subscribe("conversation-1", "user-to")
	defer unsubscribeTo()
	other, unsubscribeOther := service.Subscribe("conversation-1", "user-other")
	defer unsubscribeOther()

	payment := &Payment{ConversationID: "conversation-1", PaymentID: "payment-1", FromUserID: "user-from", ToUserID: "user-to", Status: "failed", Version: 2, UpdatedAt: time.Now()}
	service.publish(payment)
	for name, stream := range map[string]<-chan *Payment{"from": from, "to": to} {
		select {
		case got := <-stream:
			if got.PaymentID != payment.PaymentID || got.Status != "failed" {
				t.Fatalf("%s received wrong payment: %+v", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s participant did not receive payment event", name)
		}
	}
	select {
	case got := <-other:
		t.Fatalf("non-participant received payment event: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
}
