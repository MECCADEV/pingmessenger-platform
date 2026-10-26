package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	passwordLoginFailureLimit = 5
	passwordLoginFailureTTL   = 15 * time.Minute
)

type passwordLoginFailureKey struct {
	scope string
	key   string
}

func passwordLoginFailureKeys(accountID, ip string) []passwordLoginFailureKey {
	keys := make([]passwordLoginFailureKey, 0, 2)
	if accountID != "" {
		keys = append(keys, passwordLoginFailureKey{scope: "account", key: accountID})
	}
	if ip != "" {
		digest := sha256.Sum256([]byte(ip))
		keys = append(keys, passwordLoginFailureKey{scope: "ip", key: hex.EncodeToString(digest[:])})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].scope == keys[j].scope {
			return keys[i].key < keys[j].key
		}
		return keys[i].scope < keys[j].scope
	})
	return keys
}

// PasswordLoginRetryAfter reports the longest active lock across the account
// and peer IP buckets. A zero account ID checks the IP bucket alone.
func (r *UserRepository) PasswordLoginRetryAfter(ctx context.Context, accountID, ip string) (int, error) {
	keys := passwordLoginFailureKeys(accountID, ip)
	if len(keys) == 0 {
		return 0, nil
	}
	var retryAfter int
	for _, key := range keys {
		var count int
		var lockedUntil *time.Time
		err := r.pool.QueryRow(ctx, `SELECT failure_count,locked_until FROM password_login_failures WHERE scope=$1 AND key=$2`, key.scope, key.key).Scan(&count, &lockedUntil)
		if err != nil && err != pgx.ErrNoRows {
			return 0, err
		}
		if err == nil && count >= passwordLoginFailureLimit && lockedUntil != nil {
			if seconds := retrySeconds(lockedUntil.Sub(time.Now())); seconds > retryAfter {
				retryAfter = seconds
			}
		}
	}
	return retryAfter, nil
}

// RecordPasswordLoginFailure atomically records one bad password for both
// account and IP. The fifth failure starts a 15-minute lockout.
func (r *UserRepository) RecordPasswordLoginFailure(ctx context.Context, accountID, ip string) (int, error) {
	keys := passwordLoginFailureKeys(accountID, ip)
	if len(keys) == 0 {
		return 0, fmt.Errorf("password login failure requires an account or IP key")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	now := time.Now()
	// Prune expired account and IP keys opportunistically. The indexed column
	// keeps arbitrary or abandoned peer-IP buckets from accumulating forever.
	if _, err = tx.Exec(ctx, `DELETE FROM password_login_failures WHERE (locked_until IS NOT NULL AND locked_until <= $1)
		OR (locked_until IS NULL AND window_started_at <= $1 - interval '15 minutes')
		OR window_started_at <= $1 - interval '24 hours'`, now); err != nil {
		return 0, err
	}
	lockedFor := 0
	for _, key := range keys {
		var count int
		var lockedUntil *time.Time
		err = tx.QueryRow(ctx, `SELECT failure_count,locked_until FROM password_login_failures WHERE scope=$1 AND key=$2 FOR UPDATE`, key.scope, key.key).Scan(&count, &lockedUntil)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return 0, err
		}
		if count >= passwordLoginFailureLimit && lockedUntil != nil && now.Before(*lockedUntil) {
			if seconds := retrySeconds(lockedUntil.Sub(now)); seconds > lockedFor {
				lockedFor = seconds
			}
		}
	}
	if lockedFor > 0 {
		return lockedFor, nil
	}
	for _, key := range keys {
		_, err = tx.Exec(ctx, `INSERT INTO password_login_failures(scope,key,window_started_at,failure_count,locked_until) VALUES($1,$2,$3,1,NULL)
			ON CONFLICT(scope,key) DO UPDATE SET window_started_at=CASE WHEN password_login_failures.window_started_at + interval '15 minutes' <= $3 THEN $3 ELSE password_login_failures.window_started_at END,
			failure_count=CASE WHEN password_login_failures.window_started_at + interval '15 minutes' <= $3 THEN 1 ELSE LEAST(5,password_login_failures.failure_count+1) END,
			locked_until=CASE WHEN password_login_failures.window_started_at + interval '15 minutes' <= $3 THEN NULL
				WHEN password_login_failures.failure_count=4 THEN $3 + interval '15 minutes' ELSE password_login_failures.locked_until END`, key.scope, key.key, now)
		if err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return r.PasswordLoginRetryAfter(ctx, accountID, ip)
}

// ResetPasswordLoginFailures clears the account lock after a successful
// password authentication. The shared IP counter remains until its window ends.
func (r *UserRepository) ResetPasswordLoginFailures(ctx context.Context, accountID string) (int64, error) {
	if accountID == "" {
		return 0, nil
	}
	result, err := r.pool.Exec(ctx, `DELETE FROM password_login_failures WHERE scope='account' AND key=$1`, accountID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func retrySeconds(remaining time.Duration) int {
	if remaining <= 0 {
		return 0
	}
	return int(math.Ceil(remaining.Seconds()))
}
