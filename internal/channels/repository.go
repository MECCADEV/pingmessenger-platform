package channels

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Create(ctx context.Context, p CreateParams) (*Channel, error) {
	channel := new(Channel)
	err := r.pool.QueryRow(ctx, `INSERT INTO channels(openim_group_id,created_by,name,description,photo_url,visibility)
		VALUES($1,$2,$3,$4,$5,$6)
		RETURNING id::text,openim_group_id,created_by::text,name,description,photo_url,visibility,created_at,updated_at`,
		p.OpenIMGroupID, p.CreatedBy, p.Name, p.Description, p.PhotoURL, string(p.Visibility)).Scan(
		&channel.ID, &channel.OpenIMGroupID, &channel.CreatedBy, &channel.Name, &channel.Description,
		&channel.PhotoURL, &channel.Visibility, &channel.CreatedAt, &channel.UpdatedAt)
	return channel, err
}

func (r *Repository) ByID(ctx context.Context, id string) (*Channel, error) {
	channel := new(Channel)
	err := r.pool.QueryRow(ctx, `SELECT id::text,openim_group_id,created_by::text,name,description,photo_url,visibility,created_at,updated_at
		FROM channels WHERE id=$1`, id).Scan(
		&channel.ID, &channel.OpenIMGroupID, &channel.CreatedBy, &channel.Name, &channel.Description,
		&channel.PhotoURL, &channel.Visibility, &channel.CreatedAt, &channel.UpdatedAt)
	if err == nil && channel.ID == "" {
		return nil, pgx.ErrNoRows
	}
	return channel, err
}

func (r *Repository) SearchPublic(ctx context.Context, query string, limit, offset int) ([]*Channel, int, error) {
	query = strings.TrimSpace(query)
	pattern := "%" + escapeLike(query) + "%"
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM channels WHERE visibility='public' AND ($1='' OR name ILIKE $2 ESCAPE E'\\' OR description ILIKE $2 ESCAPE E'\\')`, query, pattern).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text,openim_group_id,created_by::text,name,description,photo_url,visibility,created_at,updated_at
		FROM channels WHERE visibility='public' AND ($1='' OR name ILIKE $2 ESCAPE E'\\' OR description ILIKE $2 ESCAPE E'\\')
		ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, query, pattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	channels := make([]*Channel, 0, limit)
	for rows.Next() {
		channel := new(Channel)
		if err = rows.Scan(&channel.ID, &channel.OpenIMGroupID, &channel.CreatedBy, &channel.Name, &channel.Description,
			&channel.PhotoURL, &channel.Visibility, &channel.CreatedAt, &channel.UpdatedAt); err != nil {
			return nil, 0, err
		}
		channels = append(channels, channel)
	}
	return channels, total, rows.Err()
}

func (r *Repository) Pin(ctx context.Context, channelID, messageID, actorID string) (*Pin, bool, error) {
	pin := new(Pin)
	err := r.pool.QueryRow(ctx, `INSERT INTO channel_pinned_posts(channel_id,openim_message_id,pinned_by)
		VALUES($1,$2,$3) ON CONFLICT(channel_id,openim_message_id) DO NOTHING
		RETURNING channel_id::text,openim_message_id,pinned_by::text,pinned_at`, channelID, messageID, actorID).Scan(
		&pin.ChannelID, &pin.MessageID, &pin.PinnedBy, &pin.PinnedAt)
	if err == nil {
		return pin, true, nil
	}
	if err != pgx.ErrNoRows {
		return nil, false, err
	}
	err = r.pool.QueryRow(ctx, `SELECT channel_id::text,openim_message_id,pinned_by::text,pinned_at
		FROM channel_pinned_posts WHERE channel_id=$1 AND openim_message_id=$2`, channelID, messageID).Scan(
		&pin.ChannelID, &pin.MessageID, &pin.PinnedBy, &pin.PinnedAt)
	return pin, false, err
}

func (r *Repository) Unpin(ctx context.Context, channelID, messageID string) (bool, error) {
	result, err := r.pool.Exec(ctx, `DELETE FROM channel_pinned_posts WHERE channel_id=$1 AND openim_message_id=$2`, channelID, messageID)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() > 0, nil
}

func (r *Repository) Pins(ctx context.Context, channelID string, limit int) ([]*Pin, error) {
	rows, err := r.pool.Query(ctx, `SELECT channel_id::text,openim_message_id,pinned_by::text,pinned_at
		FROM channel_pinned_posts WHERE channel_id=$1 ORDER BY pinned_at DESC,openim_message_id LIMIT $2`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pins := make([]*Pin, 0)
	for rows.Next() {
		pin := new(Pin)
		if err = rows.Scan(&pin.ChannelID, &pin.MessageID, &pin.PinnedBy, &pin.PinnedAt); err != nil {
			return nil, err
		}
		pins = append(pins, pin)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return pins, nil
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func ValidateSearchPage(limit, offset int) error {
	if limit < 1 || limit > 50 || offset < 0 || offset > 10000 {
		return fmt.Errorf("invalid channel search page")
	}
	return nil
}
