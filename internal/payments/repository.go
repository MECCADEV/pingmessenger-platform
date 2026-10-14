package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	json "github.com/goccy/go-json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return nil, ErrConflict
		}
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

func (r *Repository) RecordMessageProjection(ctx context.Context, paymentID, kind string, version int64, serverMsgID, clientMsgID string, sendTime int64) error {
	if paymentID == "" || serverMsgID == "" || clientMsgID == "" || version < 1 {
		return ErrInvalidRequest
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO payment_message_projections(payment_id,kind,version,server_msg_id,client_msg_id,send_time_ms)
		SELECT id,$2,$3,$4,$5,$6 FROM payments WHERE payment_id=$1
		ON CONFLICT (payment_id,kind,version) DO UPDATE SET server_msg_id=EXCLUDED.server_msg_id,client_msg_id=EXCLUDED.client_msg_id,send_time_ms=EXCLUDED.send_time_ms`, paymentID, kind, version, serverMsgID, clientMsgID, sendTime)
	return err
}

func (r *Repository) MessageProjectionExists(ctx context.Context, paymentID, kind string, version int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_message_projections p JOIN payments x ON x.id=p.payment_id WHERE x.payment_id=$1 AND p.kind=$2 AND p.version=$3)`, paymentID, kind, version).Scan(&exists)
	return exists, err
}

func (r *Repository) List(ctx context.Context, conversationID, userID, toID, lastKnownID, lastBlockID string, limit int) ([]*Payment, error) {
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
		  AND ($3='' OR NOT EXISTS (
			SELECT 1 FROM payments anchor
			WHERE anchor.payment_id=$3 AND anchor.conversation_id=$1
			  AND (p.created_at,p.id) > (anchor.created_at,anchor.id)
		  ))
		  AND ($4='' OR NOT EXISTS (
			SELECT 1 FROM payments anchor
			WHERE anchor.payment_id=$4 AND anchor.conversation_id=$1
			  AND (p.created_at,p.id) >= (anchor.created_at,anchor.id)
		  ))
		  AND ($5='' OR NOT EXISTS (
			SELECT 1 FROM payments anchor
			WHERE anchor.conversation_id=$1 AND anchor.block_id=$5
			  AND (p.created_at,p.id) >= (anchor.created_at,anchor.id)
		  ))
		ORDER BY p.created_at DESC,p.id DESC LIMIT $6`
	rows, err := r.pool.Query(ctx, q, conversationID, userID, toID, lastKnownID, lastBlockID, limit)
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

// FailStale atomically transitions expired or stale non-terminal payments to
// failed and appends a durable timeout event. The row lock and SKIP LOCKED
// allow multiple API replicas to run the one-second sweep safely.
func (r *Repository) FailStale(ctx context.Context, age time.Duration) ([]*Payment, error) {
	if age < 0 {
		age = 0
	}
	now := time.Now().UTC()
	cutoff := now.Add(-age)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, paymentSelect+` WHERE status IN ('pending','submitted','confirming') AND (expires_at <= $1 OR updated_at <= $2) FOR UPDATE SKIP LOCKED`, now, cutoff)
	if err != nil {
		return nil, err
	}
	var stale []*Payment
	for rows.Next() {
		p := new(Payment)
		if err := rows.Scan(paymentScanArgs(p)...); err != nil {
			rows.Close()
			return nil, err
		}
		stale = append(stale, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, p := range stale {
		version := p.Version + 1
		eventID := "timeout:" + p.PaymentID + ":" + strconv.FormatInt(version, 10)
		update := PaymentUpdate{EventID: eventID, PaymentID: p.PaymentID, ChainID: p.ChainID, TransactionID: p.TransactionID, Status: "failed", Version: version, OccurredAt: now}
		payload, err := json.Marshal(update)
		if err != nil {
			return nil, fmt.Errorf("marshal timeout event: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='failed',version=$2,updated_at=$3,last_event_at=$3 WHERE id=$1`, p.ID, version, now); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_events(event_id,payment_id,status,version,payload,occurred_at) VALUES($1,$2,'failed'::payment_status,$3,$4::jsonb,$5) ON CONFLICT (event_id) DO NOTHING`, eventID, p.ID, version, payload, now); err != nil {
			return nil, err
		}
		p.Status, p.Version, p.UpdatedAt, p.LastEventAt, p.UpdateReason = "failed", version, now, &now, "timeout"
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return stale, nil
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
		now := time.Now().UTC()
		version := p.Version + 1
		event := PaymentUpdate{EventID: "timeout:" + p.PaymentID + ":" + strconv.FormatInt(version, 10), PaymentID: p.PaymentID, ChainID: p.ChainID, TransactionID: p.TransactionID, Status: "failed", Version: version, OccurredAt: now}
		payload, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal timeout event: %w", marshalErr)
		}
		_, err = tx.Exec(ctx, `UPDATE payments SET status='failed',version=$2,updated_at=$3,last_event_at=$3 WHERE id=$1`, p.ID, version, now)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO payment_events(event_id,payment_id,status,version,payload,occurred_at) VALUES($1,$2,'failed'::payment_status,$3,$4::jsonb,$5) ON CONFLICT (event_id) DO NOTHING`, event.EventID, p.ID, version, payload, now)
		if err != nil {
			return nil, err
		}
		p.Status, p.Version, p.UpdatedAt, p.LastEventAt, p.UpdateReason = "failed", version, now, &now, "timeout"
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &p, ErrExpired
	}
	if (update.ChainID != "" && update.ChainID != p.ChainID) || (update.TransactionID != "" && update.TransactionID != p.TransactionID) {
		return &p, ErrConflict
	}
	var duplicate bool
	var duplicatePaymentID string
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_events WHERE event_id=$1), COALESCE((SELECT p.payment_id FROM payment_events e JOIN payments p ON p.id=e.payment_id WHERE e.event_id=$1),'')`, update.EventID).Scan(&duplicate, &duplicatePaymentID)
	if err != nil {
		return nil, err
	}
	if duplicate {
		if duplicatePaymentID != update.PaymentID {
			return &p, ErrConflict
		}
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
	if update.AmountAtomic != "" {
		for _, c := range update.AmountAtomic {
			if c < '0' || c > '9' {
				return ErrInvalidRequest
			}
		}
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
