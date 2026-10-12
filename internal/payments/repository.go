package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	json "github.com/goccy/go-json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Create(ctx context.Context, req CreateRequest) (*Payment, error) {
	if err := validateCreate(req); err != nil {
		return nil, err
	}
	var p Payment
	err := r.pool.QueryRow(ctx, `
		INSERT INTO payments(payment_id,conversation_id,from_user_id,to_user_id,payment_type,chain_id,transaction_id,asset,amount_atomic,expires_at)
		VALUES($1,$2,$3,$4,$5::payment_type,$6,$7,$8,$9,$10)
		ON CONFLICT (payment_id) DO NOTHING
		RETURNING id::text,payment_id,conversation_id,from_user_id::text,to_user_id::text,payment_type::text,status::text,chain_id,transaction_id,block_id,asset,amount_atomic,version,expires_at,created_at,updated_at,last_event_at`,
		req.PaymentID, req.ConversationID, req.FromUserID, req.ToUserID, req.PaymentType, req.ChainID, req.TransactionID, req.Asset, req.AmountAtomic, req.ExpiresAt,
	).Scan(paymentScanArgs(&p)...)
	if err == nil {
		return &p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// An existing payment ID is idempotent only when it represents the same
	// immutable request. This protects clients from accidental ID reuse.
	existing, err := r.Get(ctx, req.PaymentID)
	if err != nil {
		return nil, err
	}
	if existing.ConversationID != req.ConversationID || existing.FromUserID != req.FromUserID || existing.ToUserID != req.ToUserID || existing.PaymentType != req.PaymentType || existing.ChainID != req.ChainID || existing.TransactionID != req.TransactionID || existing.Asset != req.Asset || existing.AmountAtomic != req.AmountAtomic {
		return nil, ErrConflict
	}
	return existing, nil
}

func (r *Repository) Get(ctx context.Context, paymentID string) (*Payment, error) {
	var p Payment
	err := r.pool.QueryRow(ctx, paymentSelect+` WHERE payment_id=$1`, paymentID).Scan(paymentScanArgs(&p)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repository) List(ctx context.Context, conversationID, userID, toID, lastKnownID string, limit int) ([]*Payment, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	const q = `
		SELECT p.id::text,p.payment_id,p.conversation_id,p.from_user_id::text,p.to_user_id::text,p.payment_type::text,p.status::text,p.chain_id,p.transaction_id,p.block_id,p.asset,p.amount_atomic,p.version,p.expires_at,p.created_at,p.updated_at,p.last_event_at
		FROM payments p
		WHERE p.conversation_id=$1 AND (p.from_user_id=$2 OR p.to_user_id=$2)
		  AND ($3='' OR p.created_at <= COALESCE((SELECT created_at FROM payments WHERE payment_id=$3), now()))
		  AND ($4='' OR p.created_at < COALESCE((SELECT created_at FROM payments WHERE payment_id=$4), now()))
		ORDER BY p.created_at DESC,p.id DESC LIMIT $5`
	rows, err := r.pool.Query(ctx, q, conversationID, userID, toID, lastKnownID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Payment
	for rows.Next() {
		p := new(Payment)
		if err := rows.Scan(paymentScanArgs(p)...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repository) ApplyUpdate(ctx context.Context, update PaymentUpdate) (*Payment, error) {
	if err := validateUpdate(update); err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var p Payment
	err = tx.QueryRow(ctx, paymentSelect+` WHERE payment_id=$1 FOR UPDATE`, update.PaymentID).Scan(paymentScanArgs(&p)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.ExpiresAt.Before(time.Now()) && !isTerminal(p.Status) {
		_, err = tx.Exec(ctx, `UPDATE payments SET status='expired',version=version+1,updated_at=now() WHERE id=$1`, p.ID)
		if err != nil {
			return nil, err
		}
		p.Status, p.Version = "expired", p.Version+1
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &p, ErrExpired
	}
	if (update.ChainID != "" && update.ChainID != p.ChainID) || (update.TransactionID != "" && update.TransactionID != p.TransactionID) {
		return &p, ErrConflict
	}
	var duplicate bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_events WHERE event_id=$1)`, update.EventID).Scan(&duplicate)
	if err != nil {
		return nil, err
	}
	if duplicate {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &p, nil
	}
	if update.Version <= p.Version || statusRank(update.Status) < statusRank(p.Status) || (isTerminal(p.Status) && update.Status != p.Status) {
		return &p, ErrStaleUpdate
	}
	payload, err := json.Marshal(update)
	if err != nil {
		return nil, fmt.Errorf("marshal payment event: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_events(event_id,payment_id,status,version,payload,occurred_at) VALUES($1,$2,$3::payment_status,$4,$5::jsonb,$6)`, update.EventID, p.ID, update.Status, update.Version, payload, update.OccurredAt)
	if err != nil {
		return nil, err
	}
	amount := p.AmountAtomic
	if update.AmountAtomic != "" {
		amount = update.AmountAtomic
	}
	_, err = tx.Exec(ctx, `UPDATE payments SET status=$2::payment_status,block_id=COALESCE(NULLIF($3,''),block_id),amount_atomic=$4,version=$5,updated_at=now(),last_event_at=$6 WHERE id=$1`, p.ID, update.Status, update.BlockID, amount, update.Version, update.OccurredAt)
	if err != nil {
		return nil, err
	}
	if update.BlockID != "" {
		p.BlockID = update.BlockID
	}
	p.Status, p.AmountAtomic, p.Version, p.LastEventAt, p.UpdatedAt = update.Status, amount, update.Version, &update.OccurredAt, time.Now()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

const paymentSelect = `SELECT id::text,payment_id,conversation_id,from_user_id::text,to_user_id::text,payment_type::text,status::text,chain_id,transaction_id,block_id,asset,amount_atomic,version,expires_at,created_at,updated_at,last_event_at FROM payments`

func paymentScanArgs(p *Payment) []any {
	return []any{&p.ID, &p.PaymentID, &p.ConversationID, &p.FromUserID, &p.ToUserID, &p.PaymentType, &p.Status, &p.ChainID, &p.TransactionID, &p.BlockID, &p.Asset, &p.AmountAtomic, &p.Version, &p.ExpiresAt, &p.CreatedAt, &p.UpdatedAt, &p.LastEventAt}
}

func validateCreate(req CreateRequest) error {
	if req.PaymentID == "" || req.ConversationID == "" || req.FromUserID == "" || req.ToUserID == "" || req.FromUserID == req.ToUserID || req.ChainID == "" || req.TransactionID == "" || req.AmountAtomic == "" || req.ExpiresAt.IsZero() {
		return ErrInvalidRequest
	}
	if req.PaymentType != "direct" && req.PaymentType != "fund_campaign" {
		return ErrInvalidRequest
	}
	for _, c := range req.AmountAtomic {
		if c < '0' || c > '9' {
			return ErrInvalidRequest
		}
	}
	return nil
}

func validateUpdate(update PaymentUpdate) error {
	if update.EventID == "" || update.PaymentID == "" || update.Version < 1 || update.OccurredAt.IsZero() || statusRank(update.Status) == 0 {
		return ErrInvalidRequest
	}
	return nil
}

func statusRank(status string) int {
	switch status {
	case "pending":
		return 1
	case "submitted":
		return 2
	case "confirming":
		return 3
	case "confirmed", "failed", "reverted", "expired":
		return 4
	default:
		return 0
	}
}

func isTerminal(status string) bool { return statusRank(status) == 4 }
