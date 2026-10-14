// Package payments contains the platform-owned payment ledger and its boundary
// to the external PingPay-user transaction watcher.
package payments

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("payment not found")
	ErrConflict       = errors.New("payment idempotency conflict")
	ErrExpired        = errors.New("payment expired")
	ErrStaleUpdate    = errors.New("stale payment update")
	ErrUnauthorized   = errors.New("payment is not visible to this user")
	ErrInvalidRequest = errors.New("invalid payment request")
)

type Payment struct {
	ID             string     `json:"id"`
	PaymentID      string     `json:"payment_id"`
	ConversationID string     `json:"conversation_id"`
	FromUserID     string     `json:"from_user_id"`
	ToUserID       string     `json:"to_user_id"`
	PaymentType    string     `json:"payment_type"`
	Status         string     `json:"status"`
	UpdateReason   string     `json:"update_reason,omitempty"`
	ChainID        string     `json:"chain_id"`
	TransactionID  string     `json:"transaction_id"`
	BlockID        string     `json:"block_id,omitempty"`
	Asset          string     `json:"asset,omitempty"`
	AmountAtomic   string     `json:"amount_atomic"`
	Version        int64      `json:"version"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastEventAt    *time.Time `json:"last_event_at,omitempty"`
}

type CreateRequest struct {
	PaymentID      string
	ConversationID string
	FromUserID     string
	ToUserID       string
	PaymentType    string
	ChainID        string
	TransactionID  string
	Asset          string
	AmountAtomic   string
	ExpiresAt      time.Time
}

type PaymentUpdate struct {
	EventID       string    `json:"event_id"`
	PaymentID     string    `json:"payment_id"`
	ChainID       string    `json:"chain_id"`
	TransactionID string    `json:"transaction_id"`
	Status        string    `json:"status"`
	BlockID       string    `json:"block_id,omitempty"`
	Confirmations int64     `json:"confirmations,omitempty"`
	AmountAtomic  string    `json:"amount_atomic,omitempty"`
	Version       int64     `json:"version"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type WatchRequest struct {
	Action        string `json:"action"`
	PaymentID     string `json:"payment_id"`
	ChainID       string `json:"chain_id"`
	TransactionID string `json:"transaction_id"`
}

// TransactionWatcher is implemented by the PingPay-user service adapter.
// Keeping it here prevents the platform ledger from depending on its transport.
type TransactionWatcher interface {
	Watch(context.Context, WatchRequest) (<-chan PaymentUpdate, error)
	Unwatch(context.Context, WatchRequest) error
}
