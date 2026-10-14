package payments

import (
	"fmt"

	json "github.com/goccy/go-json"
)

const (
	CustomContentType  int32 = 110
	SingleChatSession  int32 = 1
	PaymentSchema            = "pingmessenger.payment.v1"
	PaymentCreatedKind       = "payment.created"
	PaymentUpdatedKind       = "payment.updated"
)

// PaymentMessageV1 is the OpenIM custom-message projection of a ledger row.
// The ledger remains authoritative; this payload is safe to replay and
// clients reconcile it by payment_id and version.
type PaymentMessageV1 struct {
	Schema         string `json:"schema"`
	Kind           string `json:"kind"`
	PaymentID      string `json:"payment_id"`
	ConversationID string `json:"conversation_id"`
	FromUserID     string `json:"from_user_id"`
	ToUserID       string `json:"to_user_id"`
	PaymentType    string `json:"payment_type"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	ChainID        string `json:"chain_id"`
	TransactionID  string `json:"transaction_id"`
	BlockID        string `json:"block_id,omitempty"`
	Asset          string `json:"asset,omitempty"`
	AmountAtomic   string `json:"amount_atomic"`
	Version        int64  `json:"version"`
	OccurredAtMs   int64  `json:"occurred_at_ms"`
}

func NewPaymentMessage(p *Payment, kind string) PaymentMessageV1 {
	when := p.UpdatedAt
	if p.LastEventAt != nil {
		when = *p.LastEventAt
	}
	return PaymentMessageV1{Schema: PaymentSchema, Kind: kind, PaymentID: p.PaymentID, ConversationID: p.ConversationID, FromUserID: p.FromUserID, ToUserID: p.ToUserID, PaymentType: p.PaymentType, Status: p.Status, Reason: p.UpdateReason, ChainID: p.ChainID, TransactionID: p.TransactionID, BlockID: p.BlockID, Asset: p.Asset, AmountAtomic: p.AmountAtomic, Version: p.Version, OccurredAtMs: when.UnixMilli()}
}

func EncodePaymentMessage(message PaymentMessageV1) ([]byte, error) {
	if message.Schema == "" {
		message.Schema = PaymentSchema
	}
	if message.Schema != PaymentSchema || (message.Kind != PaymentCreatedKind && message.Kind != PaymentUpdatedKind) || message.PaymentID == "" || message.ConversationID == "" || message.Status == "" || message.Version < 1 {
		return nil, fmt.Errorf("invalid payment message")
	}
	return json.Marshal(message)
}
