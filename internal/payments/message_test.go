package payments

import (
	"testing"
	"time"

	json "github.com/goccy/go-json"
)

func TestPaymentMessageRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	p := &Payment{PaymentID: "pay-1", ConversationID: "c-1", FromUserID: "u-1", ToUserID: "u-2", PaymentType: "direct", Status: "pending", ChainID: "solana", TransactionID: "tx-1", AmountAtomic: "100", Version: 1, CreatedAt: now, UpdatedAt: now}
	payload, err := EncodePaymentMessage(NewPaymentMessage(p, PaymentCreatedKind))
	if err != nil {
		t.Fatal(err)
	}
	var got PaymentMessageV1
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != PaymentSchema || got.Kind != PaymentCreatedKind || got.PaymentID != p.PaymentID || got.Version != 1 || got.OccurredAtMs == 0 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestEncodePaymentMessageRejectsInvalidKind(t *testing.T) {
	if _, err := EncodePaymentMessage(PaymentMessageV1{Schema: PaymentSchema, Kind: "payment.deleted", PaymentID: "p", ConversationID: "c", Status: "failed", Version: 1}); err == nil {
		t.Fatal("invalid payment message kind accepted")
	}
}
