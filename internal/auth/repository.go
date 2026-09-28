// Package auth contains raw pgx repositories for authentication data.
package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct{ pool *pgxpool.Pool }

func NewUserRepository(pool *pgxpool.Pool) *UserRepository { return &UserRepository{pool: pool} }

type CreateUserParams struct {
	Username, PasswordHash, OpenIMUserID string
	ContactKind, ContactValue            *string
}

type Contact struct {
	ID, UserID, Kind, Value string
	Verified                bool
}

func (r *UserRepository) FindContact(ctx context.Context, kind, value string) (*Contact, error) {
	c := new(Contact)
	var verifiedAt *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text, user_id::text, kind::text, value_normalized, verified_at FROM user_contacts WHERE kind=$1::contact_kind AND value_normalized=$2`, kind, value).Scan(&c.ID, &c.UserID, &c.Kind, &c.Value, &verifiedAt)
	if err != nil {
		return nil, err
	}
	c.Verified = verifiedAt != nil
	return c, nil
}
func (r *UserRepository) CreateChallenge(ctx context.Context, contact *Contact, purpose, hash string, expiresAt time.Time) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE mfa_challenges SET consumed_at=now() WHERE contact_id=$1 AND purpose=$2::challenge_purpose AND consumed_at IS NULL`, contact.ID, purpose); err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO mfa_challenges (user_id,contact_id,purpose,code_hash,expires_at) VALUES ($1,$2,$3::challenge_purpose,$4,$5) RETURNING id::text`, contact.UserID, contact.ID, purpose, hash, expiresAt).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

type Challenge struct {
	ID, UserID, ContactID, Purpose, CodeHash string
	Attempts                                 int
	ExpiresAt                                time.Time
	Consumed                                 bool
}

func (r *UserRepository) GetChallenge(ctx context.Context, id string) (*Challenge, error) {
	c := new(Challenge)
	var consumed *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text,user_id::text,contact_id::text,purpose::text,code_hash,attempts,expires_at,consumed_at FROM mfa_challenges WHERE id=$1`, id).Scan(&c.ID, &c.UserID, &c.ContactID, &c.Purpose, &c.CodeHash, &c.Attempts, &c.ExpiresAt, &consumed)
	if err != nil {
		return nil, err
	}
	c.Consumed = consumed != nil
	return c, nil
}
func (r *UserRepository) ConsumeChallenge(ctx context.Context, challenge *Challenge) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE mfa_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, challenge.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("challenge was already consumed")
	}
	if challenge.Purpose == "signup_contact_verification" {
		if _, err = tx.Exec(ctx, `UPDATE user_contacts SET verified_at=now() WHERE id=$1`, challenge.ContactID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET status='active',updated_at=now() WHERE id=$1 AND status='pending_contact_verification'`, challenge.UserID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) RecordChallengeFailure(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE mfa_challenges SET attempts=attempts+1 WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

type Session struct {
	ID, UserID string
	ExpiresAt  time.Time
}

func (r *UserRepository) CreateSession(ctx context.Context, userID, refreshHash, platform, deviceName, deviceID string, expiresAt time.Time) (*Session, error) {
	s := new(Session)
	err := r.pool.QueryRow(ctx, `INSERT INTO device_sessions (user_id,refresh_token_hash,platform_id,device_name,device_id,expires_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text,user_id::text,expires_at`, userID, refreshHash, platform, deviceName, deviceID, expiresAt).Scan(&s.ID, &s.UserID, &s.ExpiresAt)
	return s, err
}
func (r *UserRepository) RotateRefresh(ctx context.Context, oldHash, newHash string, expiresAt time.Time) (*Session, error) {
	s := new(Session)
	err := r.pool.QueryRow(ctx, `UPDATE device_sessions SET refresh_token_hash=$2,last_active_at=now(),expires_at=$3 WHERE refresh_token_hash=$1 AND revoked_at IS NULL AND expires_at>now() RETURNING id::text,user_id::text,expires_at`, oldHash, newHash, expiresAt).Scan(&s.ID, &s.UserID, &s.ExpiresAt)
	return s, err
}

type Activity struct {
	SessionID, PlatformID, DeviceName, DeviceID string
	LastActiveAt                                time.Time
}

func (r *UserRepository) ListActivity(ctx context.Context, userID string) ([]*Activity, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,platform_id,COALESCE(device_name,''),COALESCE(device_id,''),last_active_at FROM device_sessions WHERE user_id=$1 AND revoked_at IS NULL ORDER BY last_active_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Activity
	for rows.Next() {
		a := new(Activity)
		if err = rows.Scan(&a.SessionID, &a.PlatformID, &a.DeviceName, &a.DeviceID, &a.LastActiveAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *UserRepository) RevokeSessions(ctx context.Context, userID, currentID string, sessionIDs []string, all bool) error {
	if all {
		_, err := r.pool.Exec(ctx, `UPDATE device_sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`, userID, currentID)
		return err
	}
	if len(sessionIDs) == 0 {
		return fmt.Errorf("session IDs are required")
	}
	_, err := r.pool.Exec(ctx, `UPDATE device_sessions SET revoked_at=now() WHERE user_id=$1 AND id=ANY($2::uuid[]) AND revoked_at IS NULL`, userID, sessionIDs)
	return err
}
func (r *UserRepository) DiscoverOpenIMUsers(ctx context.Context, requesterID string, emails, phones []string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT u.openim_user_id FROM user_contacts c JOIN users u ON u.id=c.user_id WHERE c.user_id<>$1 AND c.verified_at IS NOT NULL AND u.status='active' AND u.openim_user_id IS NOT NULL AND ((c.kind='email' AND c.value_normalized=ANY($2)) OR (c.kind='phone' AND c.value_normalized=ANY($3)))`, requesterID, emails, phones)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *UserRepository) StoreProfileAsset(ctx context.Context, userID, key, path, contentType string, size int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE profile_assets SET deleted_at=now() WHERE user_id=$1 AND deleted_at IS NULL`, userID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO profile_assets (user_id,storage_key,public_path,content_type,byte_size) VALUES ($1,$2,$3,$4,$5)`, userID, key, path, contentType, size)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) ProfilePath(ctx context.Context, userID string) (string, error) {
	var path string
	err := r.pool.QueryRow(ctx, `SELECT public_path FROM profile_assets WHERE user_id=$1 AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`, userID).Scan(&path)
	return path, err
}

// OpenIMUserID returns the immutable identity used by the OpenIM deployment.
// Keeping it separate from the platform UUID leaves room for future account
// migrations while preventing profile updates from targeting the wrong user.
func (r *UserRepository) OpenIMUserID(ctx context.Context, userID string) (string, error) {
	var openIMUserID string
	err := r.pool.QueryRow(ctx, `SELECT openim_user_id FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&openIMUserID)
	return openIMUserID, err
}

func (r *UserRepository) SetOpenIMUserID(ctx context.Context, userID, openIMUserID string) error {
	result, err := r.pool.Exec(ctx, `UPDATE users SET openim_user_id=$2, updated_at=now() WHERE id=$1 AND openim_user_id IS NULL`, userID, openIMUserID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("OpenIM identity was already assigned or user does not exist")
	}
	return nil
}
func (r *UserRepository) RecoveryCodeCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM recovery_codes WHERE user_id=$1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}
func (r *UserRepository) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err = tx.Exec(ctx, `INSERT INTO recovery_codes (user_id,code_hash) VALUES ($1,$2)`, userID, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Create executes parameterized PostgreSQL queries through raw pgx. It accepts
// an already-derived Argon2id hash; callers must never pass plaintext passwords.
func (r *UserRepository) Create(ctx context.Context, p *CreateUserParams) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO users (username, password_hash, openim_user_id) VALUES ($1, $2, NULLIF($3, '')) RETURNING id::text`, p.Username, p.PasswordHash, p.OpenIMUserID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert user: %w", err)
	}
	if p.ContactKind != nil && p.ContactValue != nil {
		_, err = tx.Exec(ctx, `INSERT INTO user_contacts (user_id, kind, value_normalized, is_primary) VALUES ($1, $2::contact_kind, $3, true)`, id, *p.ContactKind, *p.ContactValue)
		if err != nil {
			return "", fmt.Errorf("insert user contact: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (r *UserRepository) UsernameAvailable(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE username = $1 AND deleted_at IS NULL)`, username).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check username: %w", err)
	}
	return !exists, nil
}
