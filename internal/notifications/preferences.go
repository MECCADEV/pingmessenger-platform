package notifications

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("notification preferences not found")

type Settings struct {
	Enabled                   bool   `json:"enabled"`
	Mode                      string `json:"mode"`
	Sound                     string `json:"sound"`
	MaxUpdateFrequencySeconds int    `json:"max_update_frequency_seconds"`
}

type Input struct {
	Enabled                   *bool   `json:"enabled"`
	Mode                      *string `json:"mode"`
	Sound                     *string `json:"sound"`
	MaxUpdateFrequencySeconds *int    `json:"max_update_frequency_seconds"`
}

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func Defaults() Settings {
	return Settings{Enabled: true, Mode: "all", Sound: "default", MaxUpdateFrequencySeconds: 2}
}

func (r *Repository) GetGlobal(ctx context.Context, userID string) (Settings, error) {
	value := Defaults()
	err := r.pool.QueryRow(ctx, `SELECT enabled,mode,sound,max_update_frequency_seconds
		FROM notification_global_preferences WHERE user_id=$1`, userID).Scan(
		&value.Enabled, &value.Mode, &value.Sound, &value.MaxUpdateFrequencySeconds,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, nil
	}
	return value, err
}

func (r *Repository) PutGlobal(ctx context.Context, userID string, input Input) (Settings, error) {
	_, err := r.pool.Exec(ctx, `INSERT INTO notification_global_preferences
		(user_id,enabled,mode,sound,max_update_frequency_seconds,updated_at)
		VALUES($1,COALESCE($2,true),COALESCE($3,'all'),COALESCE($4,'default'),COALESCE($5,2),now())
		ON CONFLICT(user_id) DO UPDATE SET
		enabled=COALESCE($2,notification_global_preferences.enabled),
		mode=COALESCE($3,notification_global_preferences.mode),
		sound=COALESCE($4,notification_global_preferences.sound),
		max_update_frequency_seconds=COALESCE($5,notification_global_preferences.max_update_frequency_seconds),updated_at=now()`,
		userID, input.Enabled, input.Mode, input.Sound, input.MaxUpdateFrequencySeconds)
	if err != nil {
		return Settings{}, err
	}
	return r.GetGlobal(ctx, userID)
}

func (r *Repository) GetConversation(ctx context.Context, userID, conversationID string) (Settings, error) {
	var settings Settings
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(c.enabled,g.enabled,true),
		COALESCE(c.mode,g.mode,'all'), COALESCE(c.sound,g.sound,'default'),
		COALESCE(c.max_update_frequency_seconds,g.max_update_frequency_seconds,2)
		FROM (SELECT $1::uuid AS user_id) requested
		LEFT JOIN notification_global_preferences g ON g.user_id=requested.user_id
		LEFT JOIN notification_conversation_preferences c
			ON c.user_id=requested.user_id AND c.conversation_id=$2`, userID, conversationID).Scan(
		&settings.Enabled, &settings.Mode, &settings.Sound, &settings.MaxUpdateFrequencySeconds,
	)
	return settings, err
}

func (r *Repository) PutConversation(ctx context.Context, userID, conversationID string, input Input) (Settings, error) {
	_, err := r.pool.Exec(ctx, `INSERT INTO notification_conversation_preferences
		(user_id,conversation_id,enabled,mode,sound,max_update_frequency_seconds,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,now()) ON CONFLICT(user_id,conversation_id) DO UPDATE SET
		enabled=COALESCE($3,notification_conversation_preferences.enabled),
		mode=COALESCE($4,notification_conversation_preferences.mode),
		sound=COALESCE($5,notification_conversation_preferences.sound),
		max_update_frequency_seconds=COALESCE($6,notification_conversation_preferences.max_update_frequency_seconds),updated_at=now()`,
		userID, conversationID, nullableBool(input.Enabled), nullableString(input.Mode),
		nullableString(input.Sound), nullableInt(input.MaxUpdateFrequencySeconds))
	if err != nil {
		return Settings{}, err
	}
	return r.GetConversation(ctx, userID, conversationID)
}

func (r *Repository) DeleteConversation(ctx context.Context, userID, conversationID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM notification_conversation_preferences WHERE user_id=$1 AND conversation_id=$2`, userID, conversationID)
	return err
}

func (r *Repository) Resolve(ctx context.Context, openIMUserIDs []string, conversationID string) (map[string]Settings, error) {
	rows, err := r.pool.Query(ctx, `SELECT u.openim_user_id,
		COALESCE(c.enabled,g.enabled,true), COALESCE(c.mode,g.mode,'all'),
		COALESCE(c.sound,g.sound,'default'),
		COALESCE(c.max_update_frequency_seconds,g.max_update_frequency_seconds,2)
		FROM unnest($1::text[]) AS requested(openim_user_id)
		JOIN users u ON u.openim_user_id=requested.openim_user_id
		LEFT JOIN notification_global_preferences g ON g.user_id=u.id
		LEFT JOIN notification_conversation_preferences c ON c.user_id=u.id AND c.conversation_id=$2
		WHERE u.status='active' AND u.deleted_at IS NULL`, openIMUserIDs, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resolved := make(map[string]Settings, len(openIMUserIDs))
	for rows.Next() {
		var id string
		var settings Settings
		if err := rows.Scan(&id, &settings.Enabled, &settings.Mode, &settings.Sound, &settings.MaxUpdateFrequencySeconds); err != nil {
			return nil, err
		}
		resolved[id] = settings
	}
	return resolved, rows.Err()
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
