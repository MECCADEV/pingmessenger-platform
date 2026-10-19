// Package auth contains raw pgx repositories for authentication data.
package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct{ pool *pgxpool.Pool }

func NewUserRepository(pool *pgxpool.Pool) *UserRepository { return &UserRepository{pool: pool} }

type CreateUserParams struct {
	Username, PasswordHash, OpenIMUserID string
	Nickname                             string
	ContactKind, ContactValue            *string
}

type CreatedUser struct {
	ID, PingID string
}

// PasswordLogin is the minimum account record needed to authenticate a direct
// username/password login. The Argon2id verifier never leaves this package.
type PasswordLogin struct {
	UserID, PasswordHash string
}

// ErrAccountUnavailable is returned when a session cannot be created because
// the account was soft-deleted (or is otherwise no longer active).
var ErrAccountUnavailable = errors.New("account unavailable")

type MFAFactor struct {
	ID, Kind, ContactID, ContactValue, SecretCiphertext string
	Preferred                                           bool
}

func (r *UserRepository) MFAFactor(ctx context.Context, userID, id string) (*MFAFactor, error) {
	f := new(MFAFactor)
	err := r.pool.QueryRow(ctx, `SELECT f.id::text,f.kind::text,COALESCE(f.contact_id::text,''),COALESCE(c.value_normalized,''),COALESCE(f.secret_ciphertext,''),f.preferred FROM user_mfa_factors f LEFT JOIN user_contacts c ON c.id=f.contact_id WHERE f.id=$2 AND f.user_id=$1 AND f.enabled_at IS NOT NULL AND f.disabled_at IS NULL`, userID, id).Scan(&f.ID, &f.Kind, &f.ContactID, &f.ContactValue, &f.SecretCiphertext, &f.Preferred)
	return f, err
}

func (r *UserRepository) MFAFactorEnrollment(ctx context.Context, userID, id string) (*MFAFactor, error) {
	f := new(MFAFactor)
	err := r.pool.QueryRow(ctx, `SELECT f.id::text,f.kind::text,COALESCE(f.contact_id::text,''),COALESCE(c.value_normalized,''),COALESCE(f.secret_ciphertext,''),f.preferred FROM user_mfa_factors f LEFT JOIN user_contacts c ON c.id=f.contact_id WHERE f.id=$2 AND f.user_id=$1 AND f.disabled_at IS NULL`, userID, id).Scan(&f.ID, &f.Kind, &f.ContactID, &f.ContactValue, &f.SecretCiphertext, &f.Preferred)
	return f, err
}

func (r *UserRepository) ContactForMFA(ctx context.Context, userID, kind, id string) (*Contact, error) {
	c := new(Contact)
	var verifiedAt *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text,user_id::text,kind::text,value_normalized,verified_at FROM user_contacts WHERE id=$3 AND user_id=$1 AND kind=$2::contact_kind`, userID, kind, id).Scan(&c.ID, &c.UserID, &c.Kind, &c.Value, &verifiedAt)
	c.Verified = verifiedAt != nil
	return c, err
}
func (r *UserRepository) ContactForMFAValue(ctx context.Context, userID, kind, value string) (*Contact, error) {
	c := new(Contact)
	var verifiedAt *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text,user_id::text,kind::text,value_normalized,verified_at FROM user_contacts WHERE user_id=$1 AND kind=$2::contact_kind AND value_normalized=$3`, userID, kind, value).Scan(&c.ID, &c.UserID, &c.Kind, &c.Value, &verifiedAt)
	c.Verified = verifiedAt != nil
	return c, err
}

func (r *UserRepository) CreateMFAEnrollment(ctx context.Context, userID, factorID, codeHash string, expiresAt time.Time) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO mfa_enrollment_challenges(user_id,factor_id,code_hash,expires_at) VALUES($1,$2,NULLIF($3,''),$4) RETURNING id::text`, userID, factorID, codeHash, expiresAt).Scan(&id)
	return id, err
}

type MFAEnrollment struct {
	ID, UserID, FactorID, CodeHash string
	Attempts                       int
	ExpiresAt                      time.Time
	Consumed                       bool
}

func (r *UserRepository) MFAEnrollment(ctx context.Context, id string) (*MFAEnrollment, error) {
	e := new(MFAEnrollment)
	var consumed *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text,user_id::text,factor_id::text,COALESCE(code_hash,''),attempts,expires_at,consumed_at FROM mfa_enrollment_challenges WHERE id=$1`, id).Scan(&e.ID, &e.UserID, &e.FactorID, &e.CodeHash, &e.Attempts, &e.ExpiresAt, &consumed)
	e.Consumed = consumed != nil
	return e, err
}
func (r *UserRepository) ConsumeMFAEnrollment(ctx context.Context, id string) error {
	rr, err := r.pool.Exec(ctx, `UPDATE mfa_enrollment_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL AND expires_at>now() AND attempts<5`, id)
	if err != nil {
		return err
	}
	if rr.RowsAffected() != 1 {
		return fmt.Errorf("enrollment challenge unavailable")
	}
	return nil
}
func (r *UserRepository) RecordMFAEnrollmentFailure(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE mfa_enrollment_challenges SET attempts=attempts+1 WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}
func (r *UserRepository) ActivateMFAFactor(ctx context.Context, userID, factorID string) error {
	rr, err := r.pool.Exec(ctx, `UPDATE user_mfa_factors SET enabled_at=now(),updated_at=now(),preferred=NOT EXISTS(SELECT 1 FROM user_mfa_factors WHERE user_id=$1 AND enabled_at IS NOT NULL AND disabled_at IS NULL) WHERE id=$2 AND user_id=$1 AND disabled_at IS NULL`, userID, factorID)
	if err != nil {
		return err
	}
	if rr.RowsAffected() != 1 {
		return fmt.Errorf("factor not found")
	}
	return nil
}

func (r *UserRepository) PreferredMFAFactor(ctx context.Context, userID string) (*MFAFactor, error) {
	f := new(MFAFactor)
	err := r.pool.QueryRow(ctx, `SELECT f.id::text,f.kind::text,COALESCE(f.contact_id::text,''),COALESCE(c.value_normalized,''),COALESCE(f.secret_ciphertext,''),f.preferred FROM user_mfa_factors f LEFT JOIN user_contacts c ON c.id=f.contact_id WHERE f.user_id=$1 AND f.enabled_at IS NOT NULL AND f.disabled_at IS NULL ORDER BY f.preferred DESC,f.created_at LIMIT 1`, userID).Scan(&f.ID, &f.Kind, &f.ContactID, &f.ContactValue, &f.SecretCiphertext, &f.Preferred)
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (r *UserRepository) EnableMFAFactor(ctx context.Context, userID, kind, contactID, secretCiphertext, label string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO user_mfa_factors(user_id,kind,contact_id,secret_ciphertext,label,enabled_at,preferred) VALUES($1,$2::mfa_factor_kind,NULLIF($3,'')::uuid,NULLIF($4,''),$5,now(),NOT EXISTS(SELECT 1 FROM user_mfa_factors WHERE user_id=$1 AND enabled_at IS NOT NULL AND disabled_at IS NULL)) RETURNING id::text`, userID, kind, contactID, secretCiphertext, label).Scan(&id)
	return id, err
}
func (r *UserRepository) BeginMFAFactor(ctx context.Context, userID, kind, contactID, secretCiphertext, label string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO user_mfa_factors(user_id,kind,contact_id,secret_ciphertext,label) VALUES($1,$2::mfa_factor_kind,NULLIF($3,'')::uuid,NULLIF($4,''),$5) RETURNING id::text`, userID, kind, contactID, secretCiphertext, label).Scan(&id)
	return id, err
}
func (r *UserRepository) DisableMFAFactor(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE user_mfa_factors SET disabled_at=now(),preferred=false,updated_at=now() WHERE id=$2 AND user_id=$1 AND disabled_at IS NULL`, userID, id)
	return err
}
func (r *UserRepository) MFAFactors(ctx context.Context, userID string) ([]*MFAFactor, error) {
	rows, err := r.pool.Query(ctx, `SELECT f.id::text,f.kind::text,COALESCE(f.contact_id::text,''),COALESCE(c.value_normalized,''),f.preferred FROM user_mfa_factors f LEFT JOIN user_contacts c ON c.id=f.contact_id WHERE f.user_id=$1 AND f.enabled_at IS NOT NULL AND f.disabled_at IS NULL ORDER BY f.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MFAFactor
	for rows.Next() {
		f := new(MFAFactor)
		if err = rows.Scan(&f.ID, &f.Kind, &f.ContactID, &f.ContactValue, &f.Preferred); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type PendingLogin struct {
	ID, UserID, FactorID, PlatformID, DeviceName, DeviceID, CodeHash string
	Attempts                                                         int
	ExpiresAt                                                        time.Time
	Consumed                                                         bool
}

func (r *UserRepository) CreatePendingLogin(ctx context.Context, p *PendingLogin) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO pending_login_challenges(user_id,factor_id,platform_id,device_name,device_id,code_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, p.UserID, p.FactorID, p.PlatformID, p.DeviceName, p.DeviceID, p.CodeHash, p.ExpiresAt).Scan(&id)
	return id, err
}
func (r *UserRepository) PendingLogin(ctx context.Context, id string) (*PendingLogin, error) {
	p := new(PendingLogin)
	var consumed *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text,user_id::text,factor_id::text,platform_id,COALESCE(device_name,''),COALESCE(device_id,''),COALESCE(code_hash,''),attempts,expires_at,consumed_at FROM pending_login_challenges WHERE id=$1`, id).Scan(&p.ID, &p.UserID, &p.FactorID, &p.PlatformID, &p.DeviceName, &p.DeviceID, &p.CodeHash, &p.Attempts, &p.ExpiresAt, &consumed)
	p.Consumed = consumed != nil
	if err != nil {
		return nil, err
	}
	return p, nil
}
func (r *UserRepository) ConsumePendingLogin(ctx context.Context, id string) error {
	rr, err := r.pool.Exec(ctx, `UPDATE pending_login_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL AND expires_at>now() AND attempts<5`, id)
	if err != nil {
		return err
	}
	if rr.RowsAffected() != 1 {
		return fmt.Errorf("pending login unavailable")
	}
	return nil
}
func (r *UserRepository) RecordPendingLoginFailure(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE pending_login_challenges SET attempts=attempts+1 WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

type Contact struct {
	ID, UserID, Kind, Value string
	Verified                bool
}

var (
	ErrContactAlreadyVerified = errors.New("contact is already verified and cannot be changed")
	ErrContactPending         = errors.New("another contact of this kind is pending verification")
)

// AddContact creates the user's first contact of a kind, or returns the same
// unverified contact so a new challenge can be issued. A contact kind is
// intentionally immutable once it exists: verified values cannot be replaced
// and a different pending value cannot race the original enrollment.
func (r *UserRepository) AddContact(ctx context.Context, userID, kind, value string) (*Contact, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	contact := new(Contact)
	var verifiedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,user_id::text,kind::text,value_normalized,verified_at
		FROM user_contacts WHERE user_id=$1 AND kind=$2::contact_kind
		ORDER BY is_primary DESC,created_at ASC LIMIT 1 FOR UPDATE`, userID, kind).Scan(
		&contact.ID, &contact.UserID, &contact.Kind, &contact.Value, &verifiedAt)
	if err == nil {
		contact.Verified = verifiedAt != nil
		if contact.Verified {
			return nil, ErrContactAlreadyVerified
		}
		if contact.Value != value {
			return nil, ErrContactPending
		}
		return contact, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	err = tx.QueryRow(ctx, `INSERT INTO user_contacts (user_id,kind,value_normalized,is_primary)
		VALUES ($1,$2::contact_kind,$3,true)
		RETURNING id::text,user_id::text,kind::text,value_normalized,verified_at`, userID, kind, value).Scan(
		&contact.ID, &contact.UserID, &contact.Kind, &contact.Value, &verifiedAt)
	if err != nil {
		return nil, err
	}
	contact.Verified = verifiedAt != nil
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return contact, nil
}

type PasswordChallenge struct {
	ID, UserID, Purpose, CodeHash string
	Attempts                      int
	ExpiresAt                     time.Time
	Consumed                      bool
}

func (r *UserRepository) LookupRecoveryUser(ctx context.Context, kind, value string) (string, error) {
	var userID string
	switch kind {
	case "username":
		err := r.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE username=$1 AND deleted_at IS NULL AND status='active'`, value).Scan(&userID)
		return userID, err
	case "ping_id":
		err := r.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE ping_id=$1 AND deleted_at IS NULL AND status='active'`, value).Scan(&userID)
		return userID, err
	case "email", "phone":
		err := r.pool.QueryRow(ctx, `SELECT u.id::text FROM users u JOIN user_contacts c ON c.user_id=u.id WHERE c.kind=$1::contact_kind AND c.value_normalized=$2 AND c.verified_at IS NOT NULL AND u.deleted_at IS NULL AND u.status='active'`, kind, value).Scan(&userID)
		return userID, err
	default:
		return "", fmt.Errorf("unsupported recovery identifier")
	}
}

func (r *UserRepository) VerifiedContacts(ctx context.Context, userID string) ([]*Contact, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,user_id::text,kind::text,value_normalized,true FROM user_contacts WHERE user_id=$1 AND verified_at IS NOT NULL AND kind IN ('email','phone') ORDER BY kind,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contacts []*Contact
	for rows.Next() {
		c := new(Contact)
		if err = rows.Scan(&c.ID, &c.UserID, &c.Kind, &c.Value, &c.Verified); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

func (r *UserRepository) CreatePasswordChallenge(ctx context.Context, userID, purpose, codeHash string, expiresAt time.Time) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `WITH old AS (UPDATE password_challenges SET consumed_at=now() WHERE user_id=$1 AND purpose=$2::password_challenge_purpose AND consumed_at IS NULL) INSERT INTO password_challenges(user_id,purpose,code_hash,expires_at) VALUES($1,$2::password_challenge_purpose,$3,$4) RETURNING id::text`, userID, purpose, codeHash, expiresAt).Scan(&id)
	return id, err
}

func (r *UserRepository) PasswordChallenge(ctx context.Context, id string) (*PasswordChallenge, error) {
	c := new(PasswordChallenge)
	var consumed *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text,user_id::text,purpose::text,code_hash,attempts,expires_at,consumed_at FROM password_challenges WHERE id=$1`, id).Scan(&c.ID, &c.UserID, &c.Purpose, &c.CodeHash, &c.Attempts, &c.ExpiresAt, &consumed)
	c.Consumed = consumed != nil
	return c, err
}

func (r *UserRepository) ConsumePasswordChallenge(ctx context.Context, id string) error {
	result, err := r.pool.Exec(ctx, `UPDATE password_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL AND expires_at>now() AND attempts<5`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("password challenge unavailable")
	}
	return nil
}

func (r *UserRepository) RecordPasswordChallengeFailure(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE password_challenges SET attempts=attempts+1 WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

func (r *UserRepository) ChangePassword(ctx context.Context, userID, passwordHash string) error {
	return r.changePassword(ctx, userID, "", passwordHash)
}

// ChangePasswordKeepSession rotates the password while preserving the
// authenticated session that performed the change. All other sessions are
// revoked atomically with the password update.
func (r *UserRepository) ChangePasswordKeepSession(ctx context.Context, userID, currentSessionID, passwordHash string) error {
	return r.changePassword(ctx, userID, currentSessionID, passwordHash)
}

func (r *UserRepository) changePassword(ctx context.Context, userID, currentSessionID, passwordHash string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, userID, passwordHash); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE device_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL AND ($2='' OR id<>$2::uuid)`, userID, currentSessionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *UserRepository) FindContact(ctx context.Context, kind, value string) (*Contact, error) {
	c := new(Contact)
	var verifiedAt *time.Time
	err := r.pool.QueryRow(ctx, `SELECT c.id::text, c.user_id::text, c.kind::text, c.value_normalized, c.verified_at
		FROM user_contacts c JOIN users u ON u.id=c.user_id
		WHERE c.kind=$1::contact_kind AND c.value_normalized=$2 AND u.status='active' AND u.deleted_at IS NULL`, kind, value).Scan(&c.ID, &c.UserID, &c.Kind, &c.Value, &verifiedAt)
	if err != nil {
		return nil, err
	}
	c.Verified = verifiedAt != nil
	return c, nil
}

const (
	mfaChallengeCooldown    = time.Minute
	mfaChallengeWindow      = time.Hour
	mfaChallengeHourlyLimit = 5
)

// ReserveMFAChallenge atomically reserves one challenge issuance for a
// normalized contact and requester IP. The advisory locks serialize requests
// for the same keys across API replicas without holding locks on the whole
// rate-limit table. A non-zero return value is the minimum Retry-After delay.
// A consumed challenge clears the contact cooldown so a legitimate user can
// complete one flow and immediately start another; the hourly limit remains.
func (r *UserRepository) ReserveMFAChallenge(ctx context.Context, contactID, contactKey, ipKey string, now time.Time) (time.Duration, error) {
	type rateKey struct{ scope, key string }
	keys := []rateKey{{scope: "contact", key: contactKey}, {scope: "ip", key: ipKey}}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].scope == keys[j].scope {
			return keys[i].key < keys[j].key
		}
		return keys[i].scope < keys[j].scope
	})

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	for _, key := range keys {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key.scope+":"+key.key); err != nil {
			return 0, err
		}
	}

	type state struct {
		key                         rateKey
		exists                      bool
		windowStartedAt, lastIssued time.Time
		issuedCount                 int
	}
	states := make([]state, len(keys))
	var retryAfter time.Duration
	var latestChallengeConsumed bool
	latestChallengeExists := true
	if err = tx.QueryRow(ctx, `SELECT consumed_at IS NOT NULL FROM mfa_challenges WHERE contact_id=$1 ORDER BY created_at DESC LIMIT 1`, contactID).Scan(&latestChallengeConsumed); errors.Is(err, pgx.ErrNoRows) {
		latestChallengeExists = false
	} else if err != nil {
		return 0, err
	}
	for i, key := range keys {
		states[i].key = key
		err = tx.QueryRow(ctx, `SELECT window_started_at,last_issued_at,issued_count FROM mfa_challenge_rate_limits WHERE scope=$1 AND key=$2 FOR UPDATE`, key.scope, key.key).Scan(&states[i].windowStartedAt, &states[i].lastIssued, &states[i].issuedCount)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		states[i].exists = true
		cooldownApplies := key.scope == "ip" || (states[i].exists && (!latestChallengeExists || !latestChallengeConsumed))
		if cooldownApplies {
			if elapsed := now.Sub(states[i].lastIssued); elapsed < mfaChallengeCooldown {
				if wait := mfaChallengeCooldown - elapsed; wait > retryAfter {
					retryAfter = wait
				}
			}
		}
		if now.Sub(states[i].windowStartedAt) < mfaChallengeWindow && states[i].issuedCount >= mfaChallengeHourlyLimit {
			if wait := states[i].windowStartedAt.Add(mfaChallengeWindow).Sub(now); wait > retryAfter {
				retryAfter = wait
			}
		}
	}
	if retryAfter > 0 {
		return retryAfter, nil
	}

	for _, current := range states {
		if !current.exists {
			if _, err = tx.Exec(ctx, `INSERT INTO mfa_challenge_rate_limits(scope,key,window_started_at,last_issued_at,issued_count,updated_at) VALUES($1,$2,$3,$3,1,$3)`, current.key.scope, current.key.key, now); err != nil {
				return 0, err
			}
			continue
		}
		if now.Sub(current.windowStartedAt) >= mfaChallengeWindow {
			_, err = tx.Exec(ctx, `UPDATE mfa_challenge_rate_limits SET window_started_at=$3,last_issued_at=$3,issued_count=1,updated_at=$3 WHERE scope=$1 AND key=$2`, current.key.scope, current.key.key, now)
		} else {
			_, err = tx.Exec(ctx, `UPDATE mfa_challenge_rate_limits SET last_issued_at=$3,issued_count=issued_count+1,updated_at=$3 WHERE scope=$1 AND key=$2`, current.key.scope, current.key.key, now)
		}
		if err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return 0, nil
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
	// The handler's pre-read improves error classification, but this predicate is
	// authoritative under concurrent verification requests and expiry races.
	result, err := tx.Exec(ctx, `UPDATE mfa_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL AND expires_at>now() AND attempts<5`, challenge.ID)
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

// InvalidateChallenge prevents an OTP from being used when delivery failed.
func (r *UserRepository) InvalidateChallenge(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE mfa_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

type Session struct {
	ID, UserID string
	ExpiresAt  time.Time
}

func (r *UserRepository) SessionActive(ctx context.Context, userID, sessionID string) bool {
	var active bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM device_sessions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 AND s.user_id=$2 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.status='active' AND u.deleted_at IS NULL)`, sessionID, userID).Scan(&active)
	return err == nil && active
}
func (r *UserRepository) SessionPlatform(ctx context.Context, userID, sessionID string) (string, error) {
	var platform string
	err := r.pool.QueryRow(ctx, `SELECT s.platform_id FROM device_sessions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 AND s.user_id=$2 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.status='active' AND u.deleted_at IS NULL`, sessionID, userID).Scan(&platform)
	return platform, err
}

type DeviceKey struct{ PublicJWK, Fingerprint string }

func (r *UserRepository) DeviceKey(ctx context.Context, sessionID string) (*DeviceKey, error) {
	k := new(DeviceKey)
	err := r.pool.QueryRow(ctx, `SELECT public_jwk::text,fingerprint FROM device_keys WHERE session_id=$1 AND revoked_at IS NULL`, sessionID).Scan(&k.PublicJWK, &k.Fingerprint)
	return k, err
}
func (r *UserRepository) UpsertDeviceKey(ctx context.Context, sessionID, publicJWK, fingerprint string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO device_keys(session_id,public_jwk,fingerprint) VALUES($1,$2::jsonb,$3) ON CONFLICT(session_id) DO UPDATE SET public_jwk=EXCLUDED.public_jwk,fingerprint=EXCLUDED.fingerprint,rotated_at=now(),revoked_at=NULL`, sessionID, publicJWK, fingerprint)
	return err
}

type DeviceProofChallenge struct {
	ID, SessionID, NonceHash, Audience string
	ExpiresAt                          time.Time
	Consumed                           bool
}

func (r *UserRepository) CreateDeviceProofChallenge(ctx context.Context, sessionID, nonceHash, audience string, expiresAt time.Time) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO device_proof_challenges(session_id,nonce_hash,audience,expires_at) VALUES($1,$2,$3,$4) RETURNING id::text`, sessionID, nonceHash, audience, expiresAt).Scan(&id)
	return id, err
}
func (r *UserRepository) DeviceProofChallenge(ctx context.Context, id string) (*DeviceProofChallenge, error) {
	c := new(DeviceProofChallenge)
	var consumed *time.Time
	err := r.pool.QueryRow(ctx, `SELECT id::text,session_id::text,nonce_hash,audience,expires_at,consumed_at FROM device_proof_challenges WHERE id=$1`, id).Scan(&c.ID, &c.SessionID, &c.NonceHash, &c.Audience, &c.ExpiresAt, &consumed)
	c.Consumed = consumed != nil
	return c, err
}
func (r *UserRepository) ConsumeDeviceProofChallenge(ctx context.Context, id, sessionID string) error {
	result, err := r.pool.Exec(ctx, `UPDATE device_proof_challenges SET consumed_at=now() WHERE id=$1 AND session_id=$2 AND consumed_at IS NULL AND expires_at>now()`, id, sessionID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("device proof is not usable")
	}
	return nil
}

type OpenIMDeviceToken struct{ Ciphertext string }

func (r *UserRepository) ActiveOpenIMDeviceToken(ctx context.Context, sessionID string) (*OpenIMDeviceToken, error) {
	t := new(OpenIMDeviceToken)
	err := r.pool.QueryRow(ctx, `SELECT token_ciphertext FROM openim_device_tokens WHERE session_id=$1 AND revoked_at IS NULL AND expires_at>now() ORDER BY issued_at DESC LIMIT 1`, sessionID).Scan(&t.Ciphertext)
	return t, err
}
func (r *UserRepository) ReplaceOpenIMDeviceToken(ctx context.Context, sessionID, tokenHash, ciphertext string, platformID int, expiresAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE openim_device_tokens SET revoked_at=now() WHERE session_id=$1 AND revoked_at IS NULL`, sessionID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO openim_device_tokens(session_id,token_hash,token_ciphertext,platform_id,expires_at) VALUES($1,$2,$3,$4,$5)`, sessionID, tokenHash, ciphertext, platformID, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) RevokeOpenIMDeviceToken(ctx context.Context, sessionID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE openim_device_tokens SET revoked_at=now() WHERE session_id=$1 AND revoked_at IS NULL`, sessionID)
	return err
}

func (r *UserRepository) CreateSession(ctx context.Context, userID, refreshHash, platform, deviceName, deviceID string, expiresAt time.Time) (*Session, error) {
	s := new(Session)
	err := r.pool.QueryRow(ctx, `INSERT INTO device_sessions (user_id,refresh_token_hash,platform_id,device_name,device_id,expires_at)
		SELECT u.id,$2,$3,$4,$5,$6 FROM users u
		WHERE u.id=$1 AND u.status='active' AND u.deleted_at IS NULL
		RETURNING id::text,user_id::text,expires_at`, userID, refreshHash, platform, deviceName, deviceID, expiresAt).Scan(&s.ID, &s.UserID, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountUnavailable
	}
	return s, err
}
func (r *UserRepository) RotateRefresh(ctx context.Context, oldHash, newHash string, expiresAt time.Time) (*Session, error) {
	s := new(Session)
	err := r.pool.QueryRow(ctx, `UPDATE device_sessions SET refresh_token_hash=$2,last_active_at=now(),expires_at=$3
		WHERE refresh_token_hash=$1 AND revoked_at IS NULL AND expires_at>now()
		AND EXISTS (SELECT 1 FROM users u WHERE u.id=device_sessions.user_id AND u.status='active' AND u.deleted_at IS NULL)
		RETURNING id::text,user_id::text,expires_at`, oldHash, newHash, expiresAt).Scan(&s.ID, &s.UserID, &s.ExpiresAt)
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

type ProfileAsset struct {
	StorageKey string
}

func (r *UserRepository) ActiveProfileAsset(ctx context.Context, userID string) (*ProfileAsset, error) {
	asset := new(ProfileAsset)
	err := r.pool.QueryRow(ctx, `SELECT storage_key FROM profile_assets WHERE user_id=$1 AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`, userID).Scan(&asset.StorageKey)
	return asset, err
}

// DeleteProfileAsset soft-deletes exactly the asset that the caller read. The
// storage-key predicate prevents a stale removal request from deleting a
// replacement uploaded concurrently.
func (r *UserRepository) DeleteProfileAsset(ctx context.Context, userID, storageKey string) error {
	result, err := r.pool.Exec(ctx, `UPDATE profile_assets SET deleted_at=now() WHERE user_id=$1 AND storage_key=$2 AND deleted_at IS NULL`, userID, storageKey)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("profile image not found")
	}
	return nil
}

type Profile struct {
	Username, PingID, Nickname string
	Image                      *string
}

func (r *UserRepository) Profile(ctx context.Context, userID string) (*Profile, error) {
	p := new(Profile)
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(u.username,''),u.ping_id,u.nickname, (
		SELECT public_path FROM profile_assets
		WHERE user_id=u.id AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1
	) FROM users u WHERE u.id=$1 AND u.deleted_at IS NULL`, userID).Scan(&p.Username, &p.PingID, &p.Nickname, &p.Image)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *UserRepository) UpdateNickname(ctx context.Context, userID, nickname string) error {
	result, err := r.pool.Exec(ctx, `UPDATE users SET nickname=$2, updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, userID, nickname)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (r *UserRepository) UpdateUsername(ctx context.Context, userID, username string) error {
	result, err := r.pool.Exec(ctx, `UPDATE users SET username=NULLIF($2,''), updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, userID, username)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// OpenIMUserID returns the immutable identity used by the OpenIM deployment.
// Keeping it separate from the platform UUID leaves room for future account
// migrations while preventing profile updates from targeting the wrong user.
func (r *UserRepository) OpenIMUserID(ctx context.Context, userID string) (string, error) {
	var openIMUserID string
	err := r.pool.QueryRow(ctx, `SELECT openim_user_id FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&openIMUserID)
	return openIMUserID, err
}

// PlatformUserIDsByOpenIM resolves the immutable OpenIM identities returned
// by the relation service back to Platform account UUIDs for story fan-out.
func (r *UserRepository) PlatformUserIDsByOpenIM(ctx context.Context, openIMIDs []string) (map[string]string, error) {
	result := make(map[string]string, len(openIMIDs))
	if len(openIMIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT openim_user_id,id::text FROM users WHERE openim_user_id=ANY($1::text[]) AND deleted_at IS NULL`, openIMIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var openID, platformID string
		if err = rows.Scan(&openID, &platformID); err != nil {
			return nil, err
		}
		result[openID] = platformID
	}
	return result, rows.Err()
}

func (r *UserRepository) PlatformUserIDByOpenIM(ctx context.Context, openIMID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE openim_user_id=$1 AND deleted_at IS NULL`, openIMID).Scan(&id)
	return id, err
}

// Username returns the immutable account name needed to repair a pending
// OpenIM provision. The platform UUID remains the source for the derived
// OpenIM identity; this value is presentation metadata only.
func (r *UserRepository) Username(ctx context.Context, userID string) (string, error) {
	var username string
	err := r.pool.QueryRow(ctx, `SELECT username FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&username)
	return username, err
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

type AccountDeletion struct {
	OpenIMUserID   string
	DeletedAt      time.Time
	AlreadyDeleted bool
}

// DeleteAccount performs an idempotent soft-delete. The user row is locked so
// concurrent deletion requests cannot race identity reservation or session
// revocation. No application/OpenIM/S3 row is physically removed; the user
// tombstone and revoked session timestamps block future authentication while
// preserving the account's audit history.
func (r *UserRepository) DeleteAccount(ctx context.Context, userID string) (*AccountDeletion, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var username, pingID, openIMUserID string
	var status string
	var deletedAt *time.Time
	if err = tx.QueryRow(ctx, `SELECT COALESCE(username,''),ping_id,COALESCE(openim_user_id,''),status,deleted_at FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&username, &pingID, &openIMUserID, &status, &deletedAt); err != nil {
		return nil, err
	}
	if deletedAt != nil || status == "deleted" {
		when := time.Now().UTC()
		if deletedAt != nil {
			when = deletedAt.UTC()
		}
		return &AccountDeletion{OpenIMUserID: openIMUserID, DeletedAt: when, AlreadyDeleted: true}, tx.Commit(ctx)
	}
	reserve := func(kind, value string) error {
		if value == "" {
			return nil
		}
		_, e := tx.Exec(ctx, `INSERT INTO account_identity_reservations(user_id,kind,value) VALUES($1,$2,$3) ON CONFLICT (kind,value) DO NOTHING`, userID, kind, value)
		return e
	}
	if err = reserve("ping_id", pingID); err != nil {
		return nil, err
	}
	if err = reserve("username", username); err != nil {
		return nil, err
	}
	contacts, err := tx.Query(ctx, `SELECT kind::text,value_normalized FROM user_contacts WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	for contacts.Next() {
		var kind, value string
		if err = contacts.Scan(&kind, &value); err != nil {
			contacts.Close()
			return nil, err
		}
		if err = reserve(kind, value); err != nil {
			contacts.Close()
			return nil, err
		}
	}
	if err = contacts.Err(); err != nil {
		contacts.Close()
		return nil, err
	}
	contacts.Close()
	var tombstone time.Time
	if err = tx.QueryRow(ctx, `UPDATE users SET status='deleted',deleted_at=COALESCE(deleted_at,now()),password_hash='',updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING deleted_at`, userID).Scan(&tombstone); err != nil {
		return nil, err
	}
	// Revoke all locally issued credentials in the same transaction. The
	// timestamp predicates make retries and concurrent calls idempotent.
	for _, query := range []string{
		`UPDATE device_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`,
		`UPDATE openim_device_tokens SET revoked_at=COALESCE(revoked_at,now()) WHERE session_id IN (SELECT id FROM device_sessions WHERE user_id=$1)`,
		`UPDATE device_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE session_id IN (SELECT id FROM device_sessions WHERE user_id=$1)`,
		`UPDATE device_proof_challenges SET consumed_at=COALESCE(consumed_at,now()) WHERE session_id IN (SELECT id FROM device_sessions WHERE user_id=$1)`,
		`UPDATE pending_login_challenges SET consumed_at=COALESCE(consumed_at,now()) WHERE user_id=$1`,
		`UPDATE mfa_enrollment_challenges SET consumed_at=COALESCE(consumed_at,now()) WHERE user_id=$1`,
		`UPDATE mfa_challenges SET consumed_at=COALESCE(consumed_at,now()) WHERE user_id=$1`,
		`UPDATE password_challenges SET consumed_at=COALESCE(consumed_at,now()) WHERE user_id=$1`,
		`UPDATE user_mfa_factors SET disabled_at=COALESCE(disabled_at,now()),preferred=false,updated_at=now() WHERE user_id=$1`,
	} {
		if _, err = tx.Exec(ctx, query, userID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &AccountDeletion{OpenIMUserID: openIMUserID, DeletedAt: tombstone}, nil
}

// Create executes parameterized PostgreSQL queries through raw pgx. It accepts
// an already-derived Argon2id hash; callers must never pass plaintext passwords.
func (r *UserRepository) Create(ctx context.Context, p *CreateUserParams) (*CreatedUser, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	created := new(CreatedUser)
	if p.Username != "" {
		var reserved bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_identity_reservations WHERE kind='username' AND value=$1)`, p.Username).Scan(&reserved); err != nil {
			return nil, err
		}
		if reserved {
			return nil, errors.New("identity already reserved")
		}
	}
	if p.ContactKind != nil && p.ContactValue != nil {
		var reserved bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_identity_reservations WHERE kind=$1 AND value=$2)`, *p.ContactKind, *p.ContactValue).Scan(&reserved); err != nil {
			return nil, err
		}
		if reserved {
			return nil, errors.New("identity already reserved")
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO users (username, password_hash, nickname, openim_user_id, status) VALUES (NULLIF($1,''), $2, $3, NULLIF($4, ''), 'active') RETURNING id::text,ping_id`, p.Username, p.PasswordHash, p.Nickname, p.OpenIMUserID).Scan(&created.ID, &created.PingID)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	if p.ContactKind != nil && p.ContactValue != nil {
		_, err = tx.Exec(ctx, `INSERT INTO user_contacts (user_id, kind, value_normalized, is_primary) VALUES ($1, $2::contact_kind, $3, true)`, created.ID, *p.ContactKind, *p.ContactValue)
		if err != nil {
			return nil, fmt.Errorf("insert user contact: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return created, nil
}

func (r *UserRepository) PasswordLogin(ctx context.Context, username string) (*PasswordLogin, error) {
	account := new(PasswordLogin)
	err := r.pool.QueryRow(ctx, `SELECT id::text, password_hash FROM users WHERE username=$1 AND deleted_at IS NULL AND status='active'`, username).Scan(&account.UserID, &account.PasswordHash)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (r *UserRepository) PasswordLoginByEmail(ctx context.Context, email string) (*PasswordLogin, error) {
	account := new(PasswordLogin)
	err := r.pool.QueryRow(ctx, `SELECT u.id::text, u.password_hash
		FROM users u JOIN user_contacts c ON c.user_id=u.id
		WHERE c.kind='email' AND c.value_normalized=$1 AND c.verified_at IS NOT NULL AND u.deleted_at IS NULL AND u.status='active'`, email).Scan(&account.UserID, &account.PasswordHash)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (r *UserRepository) PasswordLoginByPingID(ctx context.Context, pingID string) (*PasswordLogin, error) {
	account := new(PasswordLogin)
	err := r.pool.QueryRow(ctx, `SELECT id::text, password_hash
		FROM users
		WHERE ping_id=$1 AND deleted_at IS NULL AND status='active'`, pingID).Scan(&account.UserID, &account.PasswordHash)
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (r *UserRepository) PasswordLoginByID(ctx context.Context, userID string) (*PasswordLogin, error) {
	account := new(PasswordLogin)
	err := r.pool.QueryRow(ctx, `SELECT id::text,password_hash FROM users WHERE id=$1 AND deleted_at IS NULL AND status='active'`, userID).Scan(&account.UserID, &account.PasswordHash)
	return account, err
}

func (r *UserRepository) UsernameAvailable(ctx context.Context, username string) (bool, error) {
	var exists bool
	// Username uniqueness is lifetime-scoped. A deleted account remains a
	// tombstone, so availability must agree with the database unique index and
	// never report a name as reusable merely because its owner is soft-deleted.
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM users WHERE username = $1
		UNION ALL
		SELECT 1 FROM account_identity_reservations WHERE kind='username' AND value=$1
	)`, username).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check username: %w", err)
	}
	return !exists, nil
}
