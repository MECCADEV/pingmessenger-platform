package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"testing"
)

func TestPaymentEventSignature(t *testing.T) {
	body := []byte(`{"event_id":"e1","payment_id":"p1"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	if !validPaymentEventSignature(body, mac.Sum(nil), "secret") {
		t.Fatal("valid payment event signature rejected")
	}
	if validPaymentEventSignature(append(body, 'x'), mac.Sum(nil), "secret") {
		t.Fatal("tampered payment event accepted")
	}
}
