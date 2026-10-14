// Package timers contains the raw-pgx repository for operator-controlled
// global timers. Timer values are deliberately not accepted from clients.
package timers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("timer not found")

type Timer struct {
	Key             string `json:"key"`
	DurationSeconds int    `json:"duration_seconds"`
	Enabled         bool   `json:"enabled"`
}

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Get(ctx context.Context, key string) (*Timer, error) {
	t := new(Timer)
	err := r.pool.QueryRow(ctx, `SELECT timer_key,duration_seconds,enabled FROM global_timers WHERE timer_key=$1`, key).Scan(&t.Key, &t.DurationSeconds, &t.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *Repository) List(ctx context.Context) ([]*Timer, error) {
	rows, err := r.pool.Query(ctx, `SELECT timer_key,duration_seconds,enabled FROM global_timers ORDER BY timer_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Timer
	for rows.Next() {
		t := new(Timer)
		if err := rows.Scan(&t.Key, &t.DurationSeconds, &t.Enabled); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
