package stories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCursorRegression = errors.New("story cursor cannot move backwards")
var ErrNotFound = errors.New("story not found")
var ErrForbidden = errors.New("story is not available to this user")
var ErrActiveLimit = errors.New("maximum active stories reached")
var ErrTotalLimit = errors.New("maximum retained stories reached")

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

type CreateParams struct {
	OwnerID, MediaType, StorageKey, MediaURL, ContentType, Caption, Visibility string
	ByteSize                                                                   int64
	ExpiresAt                                                                  time.Time
	Recipients                                                                 []string
	VisibilityUsers                                                            []string
}

type Expired struct {
	ID         string
	StorageKey string
}

type Delivery struct {
	RecipientID string
	Event       *Event
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (r *Repository) Create(ctx context.Context, p CreateParams) (*Story, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Serialize creates for this owner so concurrent requests cannot exceed the
	// active-story limit. Expired rows do not count even if the cleanup ticker
	// has not marked them deleted yet.
	var lockedOwner string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, p.OwnerID).Scan(&lockedOwner); err != nil {
		return nil, err
	}
	var active int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM stories WHERE owner_id=$1 AND deleted_at IS NULL AND expires_at>now()`, p.OwnerID).Scan(&active); err != nil {
		return nil, err
	}
	if active >= MaxActiveStories {
		return nil, ErrActiveLimit
	}
	var total int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM stories WHERE owner_id=$1`, p.OwnerID).Scan(&total); err != nil {
		return nil, err
	}
	if total >= MaxTotalStories {
		return nil, ErrTotalLimit
	}
	var s Story
	err = tx.QueryRow(ctx, `INSERT INTO stories (owner_id,media_type,storage_key,media_url,content_type,byte_size,caption,visibility,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id::text,owner_id::text,media_type::text,media_url,content_type,byte_size,caption,visibility::text,expires_at,created_at`,
		p.OwnerID, p.MediaType, p.StorageKey, p.MediaURL, p.ContentType, p.ByteSize, p.Caption, p.Visibility, p.ExpiresAt).Scan(
		&s.ID, &s.OwnerID, &s.MediaType, &s.MediaURL, &s.ContentType, &s.ByteSize, &s.Caption, &s.Visibility, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	s.StorageKey = p.StorageKey
	for _, id := range p.VisibilityUsers {
		if _, err = tx.Exec(ctx, `INSERT INTO story_visibility_users (story_id,user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, s.ID, id); err != nil {
			return nil, err
		}
	}
	seen := make(map[string]struct{}, len(p.Recipients)+1)
	for _, id := range append(p.Recipients, p.OwnerID) {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, err = tx.Exec(ctx, `INSERT INTO story_recipients (story_id,recipient_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, s.ID, id); err != nil {
			return nil, err
		}
		var eventID int64
		if err = tx.QueryRow(ctx, `INSERT INTO story_events (story_id,owner_id,recipient_id,event_type) VALUES ($1,$2,$3,'created') RETURNING event_id`, s.ID, p.OwnerID, id).Scan(&eventID); err != nil {
			return nil, err
		}
		if err = notifyStoryEvent(ctx, tx, eventID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repository) Delete(ctx context.Context, ownerID, storyID string) (*Story, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var s Story
	var deletedAt time.Time
	err = tx.QueryRow(ctx, `UPDATE stories SET deleted_at=COALESCE(deleted_at,now()),media_cleanup_claimed_at=now(),updated_at=now()
		WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL
		RETURNING id::text,owner_id::text,storage_key,media_type::text,media_url,content_type,byte_size,caption,visibility::text,expires_at,deleted_at,created_at`, storyID, ownerID).Scan(
		&s.ID, &s.OwnerID, &s.StorageKey, &s.MediaType, &s.MediaURL, &s.ContentType, &s.ByteSize, &s.Caption, &s.Visibility, &s.ExpiresAt, &deletedAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.DeletedAt = &deletedAt
	rows, err := tx.Query(ctx, `INSERT INTO story_events (story_id,owner_id,recipient_id,event_type)
		SELECT sr.story_id,s.owner_id,sr.recipient_id,'deleted' FROM story_recipients sr JOIN stories s ON s.id=sr.story_id WHERE sr.story_id=$1 RETURNING event_id`, storyID)
	if err != nil {
		return nil, err
	}
	eventIDs := make([]int64, 0)
	for rows.Next() {
		var eventID int64
		if err = rows.Scan(&eventID); err != nil {
			rows.Close()
			return nil, err
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, eventID := range eventIDs {
		if err = notifyStoryEvent(ctx, tx, eventID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repository) View(ctx context.Context, viewerID, storyID, reaction string) (*View, error) {
	var v View
	err := r.pool.QueryRow(ctx, `INSERT INTO story_views (story_id,viewer_id,reaction)
		SELECT $1,$2,$3 WHERE EXISTS (SELECT 1 FROM story_recipients sr JOIN stories s ON s.id=sr.story_id WHERE sr.story_id=$1 AND sr.recipient_id=$2 AND s.deleted_at IS NULL AND s.expires_at>now())
		ON CONFLICT (story_id,viewer_id) DO UPDATE SET viewed_at=now(),reaction=EXCLUDED.reaction
		RETURNING viewer_id::text,viewed_at,reaction`, storyID, viewerID, reaction).Scan(&v.UserID, &v.ViewedAt, &v.Reaction)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	}
	return &v, err
}

func (r *Repository) MediaForRecipient(ctx context.Context, userID, storyID string) (*Story, error) {
	var s Story
	err := r.pool.QueryRow(ctx, `SELECT s.id::text,s.owner_id::text,s.storage_key,s.media_type::text,s.media_url,s.content_type,s.byte_size,s.caption,s.visibility::text,s.expires_at,s.deleted_at,s.created_at
		FROM stories s JOIN story_recipients sr ON sr.story_id=s.id
		WHERE s.id=$1 AND sr.recipient_id=$2 AND s.deleted_at IS NULL AND s.expires_at>now()`, storyID, userID).Scan(
		&s.ID, &s.OwnerID, &s.StorageKey, &s.MediaType, &s.MediaURL, &s.ContentType, &s.ByteSize, &s.Caption, &s.Visibility, &s.ExpiresAt, &s.DeletedAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrForbidden
	}
	return &s, err
}

func (r *Repository) Views(ctx context.Context, ownerID, storyID string) ([]*View, error) {
	rows, err := r.pool.Query(ctx, `SELECT v.viewer_id::text,v.viewed_at,v.reaction FROM story_views v JOIN stories s ON s.id=v.story_id WHERE v.story_id=$1 AND s.owner_id=$2 ORDER BY v.viewed_at ASC`, storyID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	views := make([]*View, 0)
	for rows.Next() {
		v := new(View)
		if err = rows.Scan(&v.UserID, &v.ViewedAt, &v.Reaction); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

func (r *Repository) Sync(ctx context.Context, userID string, global int64, users map[string]int64, limit int) (*SyncResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	stored, err := r.cursors(ctx, userID)
	if err != nil {
		return nil, err
	}
	if previous, ok := stored["global"]; ok && global < previous {
		return nil, ErrCursorRegression
	}
	for owner, cursor := range users {
		if previous, ok := stored[owner]; ok && cursor < previous {
			return nil, ErrCursorRegression
		}
	}
	args := []any{userID, global}
	where := []string{"(e.recipient_id=$1 AND e.event_id>$2)"}
	argN := 3
	for owner, cursor := range users {
		if cursor < 0 {
			return nil, fmt.Errorf("cursor must be non-negative")
		}
		where = append(where, fmt.Sprintf("(e.recipient_id=$1 AND e.owner_id=$%d AND e.event_id>$%d)", argN, argN+1))
		args = append(args, owner, cursor)
		argN += 2
	}
	args = append(args, limit+1)
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT e.event_id,e.event_type,e.story_id::text,e.owner_id::text,e.created_at,
		s.media_type::text,s.storage_key,s.media_url,s.content_type,s.byte_size,s.caption,s.visibility::text,s.expires_at,s.deleted_at,s.created_at
		FROM story_events e LEFT JOIN stories s ON s.id=e.story_id
		WHERE (`+strings.Join(where, " OR ")+`) AND (e.event_type='deleted' OR (s.deleted_at IS NULL AND s.expires_at>now())) ORDER BY e.event_id LIMIT $`+fmt.Sprint(argN), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &SyncResult{Events: make([]*Event, 0), GlobalCursor: global, UserCursors: make(map[string]int64, len(users))}
	for k, v := range users {
		out.UserCursors[k] = v
	}
	for rows.Next() {
		if len(out.Events) >= limit {
			out.HasMore = true
			break
		}
		var e Event
		var typ, storyID, owner string
		var mediaType, storageKey, mediaURL, contentType, caption, visibility *string
		var size *int64
		var expires, deleted, storyCreated *time.Time
		if err = rows.Scan(&e.EventID, &typ, &storyID, &owner, &e.CreatedAt, &mediaType, &storageKey, &mediaURL, &contentType, &size, &caption, &visibility, &expires, &deleted, &storyCreated); err != nil {
			return nil, err
		}
		e.Type, e.StoryID, e.OwnerID = typ, storyID, owner
		if deleted != nil {
			e.DeletedAt = deleted
		}
		if typ == "created" && storageKey != nil {
			e.Story = &Story{ID: storyID, OwnerID: owner, StorageKey: *storageKey, MediaType: MediaType(*mediaType), MediaURL: valueOrEmpty(mediaURL), ContentType: *contentType, ByteSize: *size, Caption: *caption, Visibility: Visibility(*visibility), ExpiresAt: *expires, DeletedAt: deleted, CreatedAt: *storyCreated}
		}
		out.Events = append(out.Events, &e)
		if e.EventID > out.GlobalCursor {
			out.GlobalCursor = e.EventID
		}
		if current, ok := out.UserCursors[owner]; !ok || e.EventID > current {
			out.UserCursors[owner] = e.EventID
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) cursors(ctx context.Context, userID string) (map[string]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT scope_key,cursor FROM story_sync_cursors WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var scope string
		var cursor int64
		if err = rows.Scan(&scope, &cursor); err != nil {
			return nil, err
		}
		result[scope] = cursor
	}
	return result, rows.Err()
}

func (r *Repository) Ack(ctx context.Context, userID string, global int64, users map[string]int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	values := map[string]int64{"global": global}
	for owner, cursor := range users {
		values[owner] = cursor
	}
	for scope, cursor := range values {
		var old int64
		err = tx.QueryRow(ctx, `SELECT cursor FROM story_sync_cursors WHERE user_id=$1 AND scope_key=$2 FOR UPDATE`, userID, scope).Scan(&old)
		if errors.Is(err, pgx.ErrNoRows) {
			old = 0
			err = nil
		}
		if err != nil {
			return err
		}
		if cursor < old {
			return ErrCursorRegression
		}
		if _, err = tx.Exec(ctx, `INSERT INTO story_sync_cursors(user_id,scope_key,cursor) VALUES($1,$2,$3) ON CONFLICT(user_id,scope_key) DO UPDATE SET cursor=EXCLUDED.cursor,updated_at=now()`, userID, scope, cursor); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) Expire(ctx context.Context) ([]Expired, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `UPDATE stories SET deleted_at=now(),media_cleanup_claimed_at=now(),updated_at=now() WHERE expires_at<=now() AND deleted_at IS NULL RETURNING id,storage_key`)
	if err != nil {
		return nil, err
	}
	var expired []Expired
	for rows.Next() {
		var item Expired
		if err = rows.Scan(&item.ID, &item.StorageKey); err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, item)
	}
	rows.Close()
	for _, item := range expired {
		rows, err = tx.Query(ctx, `INSERT INTO story_events(story_id,owner_id,recipient_id,event_type) SELECT sr.story_id,s.owner_id,sr.recipient_id,'deleted' FROM story_recipients sr JOIN stories s ON s.id=sr.story_id WHERE sr.story_id=$1 RETURNING event_id`, item.ID)
		if err != nil {
			return nil, err
		}
		eventIDs := make([]int64, 0)
		for rows.Next() {
			var eventID int64
			if err = rows.Scan(&eventID); err != nil {
				rows.Close()
				return nil, err
			}
			eventIDs = append(eventIDs, eventID)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		for _, eventID := range eventIDs {
			if err = notifyStoryEvent(ctx, tx, eventID); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return expired, nil
}

func notifyStoryEvent(ctx context.Context, tx pgx.Tx, eventID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_notify('story_events',$1)`, strconv.FormatInt(eventID, 10))
	return err
}

func (r *Repository) EventDelivery(ctx context.Context, eventID int64) (*Delivery, error) {
	var d Delivery
	var typ, storyID, owner string
	var mediaType, storageKey, mediaURL, contentType, caption, visibility *string
	var size *int64
	var expires, deleted, storyCreated *time.Time
	var eventCreated time.Time
	err := r.pool.QueryRow(ctx, `SELECT e.recipient_id::text,e.event_type,e.story_id::text,e.owner_id::text,e.created_at,
		s.media_type::text,s.storage_key,s.media_url,s.content_type,s.byte_size,s.caption,s.visibility::text,s.expires_at,s.deleted_at,s.created_at
		FROM story_events e LEFT JOIN stories s ON s.id=e.story_id WHERE e.event_id=$1`, eventID).Scan(
		&d.RecipientID, &typ, &storyID, &owner, &eventCreated, &mediaType, &storageKey, &mediaURL, &contentType, &size, &caption, &visibility, &expires, &deleted, &storyCreated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Event = &Event{EventID: eventID, Type: typ, StoryID: storyID, OwnerID: owner, CreatedAt: eventCreated, DeletedAt: deleted}
	if typ == "created" && storageKey != nil {
		d.Event.Story = &Story{ID: storyID, OwnerID: owner, StorageKey: *storageKey, MediaType: MediaType(*mediaType), MediaURL: valueOrEmpty(mediaURL), ContentType: *contentType, ByteSize: *size, Caption: *caption, Visibility: Visibility(*visibility), ExpiresAt: *expires, DeletedAt: deleted, CreatedAt: *storyCreated}
	}
	return &d, nil
}

func (r *Repository) ListenStoryEvents(ctx context.Context, handler func(context.Context, int64)) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `LISTEN story_events`); err != nil {
		return err
	}
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		eventID, err := strconv.ParseInt(notification.Payload, 10, 64)
		if err != nil || eventID <= 0 {
			continue
		}
		handler(ctx, eventID)
	}
}

func (r *Repository) CleanupDeleted(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	result, err := r.pool.Exec(ctx, `WITH doomed AS (
		SELECT id FROM stories WHERE deleted_at IS NOT NULL AND deleted_at<$1 AND media_deleted_at IS NOT NULL ORDER BY deleted_at LIMIT $2
	) DELETE FROM stories s USING doomed WHERE s.id=doomed.id`, before, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// PendingMediaCleanup returns soft-deleted stories whose object-store delete
// has not yet been recorded as successful. The API retries these keys on each
// expiry sweep, making S3 cleanup resilient to transient outages.
func (r *Repository) PendingMediaCleanup(ctx context.Context, limit int) ([]Expired, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `WITH picked AS (
		SELECT id FROM stories
		WHERE deleted_at IS NOT NULL AND media_deleted_at IS NULL
		  AND (media_cleanup_claimed_at IS NULL OR media_cleanup_claimed_at<now()-interval '5 minutes')
		ORDER BY deleted_at FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE stories s SET media_cleanup_claimed_at=now(),updated_at=now()
	FROM picked WHERE s.id=picked.id
	RETURNING s.id::text,s.storage_key`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Expired, 0)
	for rows.Next() {
		var item Expired
		if err = rows.Scan(&item.ID, &item.StorageKey); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) MarkMediaDeleted(ctx context.Context, storyID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE stories SET media_deleted_at=COALESCE(media_deleted_at,now()),media_cleanup_claimed_at=NULL,updated_at=now() WHERE id=$1`, storyID)
	return err
}

func (r *Repository) RecordMediaCleanupError(ctx context.Context, storyID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE stories SET media_cleanup_claimed_at=NULL,updated_at=now() WHERE id=$1`, storyID)
	return err
}
