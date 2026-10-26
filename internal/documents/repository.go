package documents

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCountLimit          = errors.New("document count limit reached")
	ErrQuotaLimit          = errors.New("document upload quota reached")
	ErrNotFound            = errors.New("document not found")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for a different document")
	ErrIdempotencyGone     = errors.New("idempotent document result is no longer available")
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Reserve serializes reservations per user and records the document before
// the S3 write. A failed object write is moved to the normal cleanup path.
func (r *Repository) Reserve(ctx context.Context, ownerID, key, name, contentType string, size int64, policy Policy) (*Document, error) {
	doc, _, err := r.ReserveIdempotent(ctx, ownerID, key, name, contentType, size, policy, "", "")
	return doc, err
}

// ReserveIdempotent binds a client key and request fingerprint to the reserved
// document in the same transaction. Replays return the original row before
// quota accounting, including tombstones so deleted uploads cannot resurrect.
func (r *Repository) ReserveIdempotent(ctx context.Context, ownerID, key, name, contentType string, size int64, policy Policy, idempotencyKey, requestHash string) (*Document, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, ownerID); err != nil {
		return nil, false, err
	}
	if idempotencyKey != "" {
		var existing Document
		var storedHash string
		var deletedAt *time.Time
		lookupErr := tx.QueryRow(ctx, `SELECT d.id::text,d.owner_id::text,d.storage_key,d.file_name,d.content_type,d.byte_size,d.created_at,d.expires_at,d.deleted_at,u.request_hash
			FROM message_documents d JOIN message_document_upload_idempotency u ON u.document_id=d.id
			WHERE d.owner_id=$1 AND u.idempotency_key=$2 FOR UPDATE OF d,u`, ownerID, idempotencyKey).Scan(
			&existing.ID, &existing.OwnerID, &existing.StorageKey, &existing.FileName, &existing.ContentType, &existing.ByteSize, &existing.CreatedAt, &existing.ExpiresAt, &deletedAt, &storedHash)
		if lookupErr == nil {
			if storedHash != requestHash {
				return nil, false, ErrIdempotencyConflict
			}
			existing.DeletedAt = deletedAt
			if deletedAt != nil || !existing.ExpiresAt.After(time.Now()) {
				return nil, false, ErrIdempotencyGone
			}
			if err = tx.Commit(ctx); err != nil {
				return nil, false, err
			}
			return &existing, false, nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return nil, false, lookupErr
		}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM message_documents WHERE owner_id=$1 AND deleted_at IS NULL AND expires_at>now()`, ownerID).Scan(&count); err != nil {
		return nil, false, err
	}
	if count >= policy.MaxCount {
		return nil, false, ErrCountLimit
	}
	var bytes int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(byte_size),0) FROM message_documents WHERE owner_id=$1 AND created_at>now()-interval '24 hours'`, ownerID).Scan(&bytes); err != nil {
		return nil, false, err
	}
	if bytes > policy.MaxBytes24h-size {
		return nil, false, ErrQuotaLimit
	}
	var d Document
	err = tx.QueryRow(ctx, `INSERT INTO message_documents (owner_id,storage_key,file_name,content_type,byte_size,expires_at)
		VALUES ($1,$2,$3,$4,$5,now()+$6::interval)
		RETURNING id::text,owner_id::text,storage_key,file_name,content_type,byte_size,created_at,expires_at`, ownerID, key, name, contentType, size, policy.Retention.String()).Scan(
		&d.ID, &d.OwnerID, &d.StorageKey, &d.FileName, &d.ContentType, &d.ByteSize, &d.CreatedAt, &d.ExpiresAt)
	if err != nil {
		return nil, false, err
	}
	if idempotencyKey != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO message_document_upload_idempotency(owner_id,idempotency_key,request_hash,document_id) VALUES ($1,$2,$3,$4)`, ownerID, idempotencyKey, requestHash, d.ID); err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &d, true, nil
}

func (r *Repository) MarkDeleteRequested(ctx context.Context, ownerID, id string) (*CleanupItem, error) {
	var item CleanupItem
	err := r.pool.QueryRow(ctx, `UPDATE message_documents SET deleted_at=COALESCE(deleted_at,now()),cleanup_claimed_at=now(),updated_at=now()
		WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL RETURNING id::text,storage_key`, id, ownerID).Scan(&item.ID, &item.StorageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

func (r *Repository) ClaimExpired(ctx context.Context, limit int) ([]CleanupItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `WITH picked AS (
		SELECT id FROM message_documents
		WHERE deleted_at IS NULL AND expires_at<=now()
		ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE message_documents d SET deleted_at=COALESCE(d.deleted_at,now()),cleanup_claimed_at=now(),updated_at=now()
	FROM picked WHERE d.id=picked.id
	RETURNING d.id::text,d.storage_key`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCleanup(rows)
}

func (r *Repository) PendingCleanup(ctx context.Context, limit int) ([]CleanupItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `WITH picked AS (
		SELECT id FROM message_documents
		WHERE deleted_at IS NOT NULL AND s3_deleted_at IS NULL
		  AND (cleanup_claimed_at IS NULL OR cleanup_claimed_at<now()-interval '5 minutes')
		ORDER BY deleted_at FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE message_documents d SET cleanup_claimed_at=now(),updated_at=now()
	FROM picked WHERE d.id=picked.id
	RETURNING d.id::text,d.storage_key`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCleanup(rows)
}

func scanCleanup(rows pgx.Rows) ([]CleanupItem, error) {
	items := make([]CleanupItem, 0)
	for rows.Next() {
		var item CleanupItem
		if err := rows.Scan(&item.ID, &item.StorageKey); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) MarkS3Deleted(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE message_documents SET s3_deleted_at=COALESCE(s3_deleted_at,now()),cleanup_claimed_at=NULL,updated_at=now(),last_delete_error=NULL WHERE id=$1`, id)
	return err
}

func (r *Repository) LinkMessage(ctx context.Context, ownerID, documentID, conversationID, openIMUserID string, seq int64, expiresAt time.Time) error {
	var linkID string
	err := r.pool.QueryRow(ctx, `INSERT INTO message_media_links(document_id,owner_id,conversation_id,seq,openim_user_id,expires_at)
		SELECT id,$1,$3,$4,$5,$6 FROM message_documents WHERE id=$2 AND owner_id=$1 AND deleted_at IS NULL AND expires_at>now()
		ON CONFLICT (document_id,conversation_id,seq) DO UPDATE SET expires_at=EXCLUDED.expires_at,updated_at=now()
		RETURNING id::text`, ownerID, documentID, conversationID, seq, openIMUserID, expiresAt).Scan(&linkID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (r *Repository) ClaimExpiredMessageLinks(ctx context.Context, limit int) ([]MessageMediaLink, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `WITH picked AS (
		SELECT id FROM message_media_links
		WHERE expires_at<=now() AND (revoked_at IS NULL OR s3_deleted_at IS NULL)
		  AND (cleanup_claimed_at IS NULL OR cleanup_claimed_at<now()-interval '5 minutes')
		ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE message_media_links l SET cleanup_claimed_at=now(),revoke_attempts=revoke_attempts+1,updated_at=now()
	FROM picked p, message_documents d
	WHERE l.id=p.id AND d.id=l.document_id
		RETURNING l.id::text,l.document_id::text,d.storage_key,l.conversation_id,l.openim_user_id,l.seq,l.revoked_at IS NOT NULL,l.s3_deleted_at IS NOT NULL`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := make([]MessageMediaLink, 0)
	for rows.Next() {
		var link MessageMediaLink
		if err := rows.Scan(&link.ID, &link.DocumentID, &link.StorageKey, &link.ConversationID, &link.OpenIMUserID, &link.Seq, &link.Revoked, &link.S3Deleted); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func (r *Repository) MarkMessageRevoked(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE message_media_links SET revoked_at=COALESCE(revoked_at,now()),last_error=NULL,updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *Repository) MarkMessageS3Deleted(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE message_media_links SET s3_deleted_at=COALESCE(s3_deleted_at,now()),updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *Repository) RecordMessageCleanupError(ctx context.Context, id, message string) error {
	_, err := r.pool.Exec(ctx, `UPDATE message_media_links SET last_error=$2,updated_at=now() WHERE id=$1`, id, message)
	return err
}

func (r *Repository) RecordDeleteError(ctx context.Context, id, message string) error {
	_, err := r.pool.Exec(ctx, `UPDATE message_documents SET delete_attempts=delete_attempts+1,cleanup_claimed_at=NULL,last_delete_error=$2,updated_at=now() WHERE id=$1`, id, message)
	return err
}

func (r *Repository) Get(ctx context.Context, ownerID, id string) (*Document, error) {
	var d Document
	err := r.pool.QueryRow(ctx, `SELECT id::text,owner_id::text,storage_key,file_name,content_type,byte_size,created_at,expires_at,deleted_at FROM message_documents WHERE id=$1 AND owner_id=$2`, id, ownerID).Scan(
		&d.ID, &d.OwnerID, &d.StorageKey, &d.FileName, &d.ContentType, &d.ByteSize, &d.CreatedAt, &d.ExpiresAt, &d.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

// Keep the interval conversion in one place so callers never need to format
// PostgreSQL-specific interval literals.
func RetentionInterval(d time.Duration) string { return d.String() }
