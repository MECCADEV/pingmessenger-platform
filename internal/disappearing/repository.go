package disappearing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("disappearing setting not found")

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Get(ctx context.Context, userID, conversationID string) (*Setting, error) {
	s := new(Setting)
	err := r.pool.QueryRow(ctx, `SELECT conversation_id,user_id::text,duration_seconds,enabled,updated_at FROM conversation_disappearing_settings WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID).Scan(&s.ConversationID, &s.UserID, &s.DurationSeconds, &s.Enabled, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (r *Repository) Upsert(ctx context.Context, userID, conversationID string, durationSeconds int, enabled bool) (*Setting, error) {
	s := new(Setting)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO conversation_disappearing_settings(conversation_id,user_id,duration_seconds,enabled)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (conversation_id,user_id) DO UPDATE SET duration_seconds=EXCLUDED.duration_seconds,enabled=EXCLUDED.enabled,updated_at=now()
		RETURNING conversation_id,user_id::text,duration_seconds,enabled,updated_at`, conversationID, userID, durationSeconds, enabled).Scan(&s.ConversationID, &s.UserID, &s.DurationSeconds, &s.Enabled, &s.UpdatedAt)
	return s, err
}

func (r *Repository) Delete(ctx context.Context, userID, conversationID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM conversation_disappearing_settings WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID)
	return err
}
