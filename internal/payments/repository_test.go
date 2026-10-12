package payments

import (
	"testing"
	"time"
)

func TestValidateCreate(t *testing.T) {
	base := CreateRequest{PaymentID: "pay-1", ConversationID: "room-1", FromUserID: "from", ToUserID: "to", PaymentType: "direct", ChainID: "solana", TransactionID: "tx-1", AmountAtomic: "100", ExpiresAt: time.Now().Add(time.Minute)}
	if err := validateCreate(base); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	base.AmountAtomic = "1.5"
	if err := validateCreate(base); err != ErrInvalidRequest {
		t.Fatalf("invalid amount error = %v", err)
	}
}

func TestStatusRankIsMonotonic(t *testing.T) {
	if statusRank("confirming") <= statusRank("submitted") {
		t.Fatal("confirming must follow submitted")
	}
	if statusRank("submitted") >= statusRank("confirmed") {
		t.Fatal("submitted must precede confirmed")
	}
	if statusRank("unknown") != 0 {
		t.Fatal("unknown status must be rejected")
	}
}
