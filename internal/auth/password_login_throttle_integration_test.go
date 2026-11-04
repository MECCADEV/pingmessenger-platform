package auth

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPasswordLoginFailuresAreSharedAndAtomic(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS password_login_failures (
		scope TEXT NOT NULL CHECK (scope IN ('account','ip')),
		key TEXT NOT NULL,
		window_started_at TIMESTAMPTZ NOT NULL,
		failure_count SMALLINT NOT NULL CHECK (failure_count > 0),
		locked_until TIMESTAMPTZ,
		PRIMARY KEY(scope,key)
	)`); err != nil {
		t.Fatalf("create throttle table: %v", err)
	}
	repo := NewUserRepository(pool)
	accountID, ip := uuid.NewString(), "198.51.100."+fmt.Sprint(time.Now().UnixNano()%200+1)
	keys := passwordLoginFailureKeys(accountID, ip)
	t.Cleanup(func() {
		for _, key := range keys {
			_, _ = pool.Exec(context.Background(), `DELETE FROM password_login_failures WHERE scope=$1 AND key=$2`, key.scope, key.key)
		}
	})

	const workers = 24
	start := make(chan struct{})
	var group sync.WaitGroup
	results := make(chan int, workers)
	errorsFound := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			retry, recordErr := repo.RecordPasswordLoginFailure(ctx, accountID, ip)
			if recordErr != nil {
				errorsFound <- recordErr
				return
			}
			results <- retry
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsFound)
	for recordErr := range errorsFound {
		t.Fatalf("record concurrent failure: %v", recordErr)
	}
	blocked := 0
	for retry := range results {
		if retry > 0 {
			blocked++
			if retry > int(passwordLoginFailureTTL.Seconds()) {
				t.Fatalf("retry_after_seconds=%d exceeds 15-minute cooldown", retry)
			}
		}
	}
	if blocked < workers-4 {
		t.Fatalf("blocked calls=%d, want at least %d (five failures counted atomically)", blocked, workers-4)
	}
	for _, key := range keys {
		var count int
		if err = pool.QueryRow(ctx, `SELECT failure_count FROM password_login_failures WHERE scope=$1 AND key=$2`, key.scope, key.key).Scan(&count); err != nil || count != passwordLoginFailureLimit {
			t.Fatalf("%s failure count=%d err=%v, want %d", key.scope, count, err, passwordLoginFailureLimit)
		}
	}

	if _, err = repo.ResetPasswordLoginFailures(ctx, accountID); err != nil {
		t.Fatalf("reset account failures: %v", err)
	}
	if retry, checkErr := repo.PasswordLoginRetryAfter(ctx, accountID, "203.0.113.8"); checkErr != nil || retry != 0 {
		t.Fatalf("account retry_after=%d err=%v after successful login reset, want 0", retry, checkErr)
	}
	if retry, checkErr := repo.PasswordLoginRetryAfter(ctx, "", ip); checkErr != nil || retry <= 0 {
		t.Fatalf("shared IP retry_after=%d err=%v, want active lock after account reset", retry, checkErr)
	}
	if _, err = pool.Exec(ctx, `UPDATE password_login_failures SET window_started_at=now()-interval '16 minutes',locked_until=now()-interval '1 second' WHERE scope='ip' AND key=$1`, keys[1].key); err != nil {
		t.Fatalf("age IP failure window: %v", err)
	}
	if retry, recordErr := repo.RecordPasswordLoginFailure(ctx, "", ip); recordErr != nil || retry != 0 {
		t.Fatalf("expired window retry_after=%d err=%v, want a fresh first failure", retry, recordErr)
	}
	var staleRows int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM password_login_failures WHERE window_started_at + interval '15 minutes' <= now()`).Scan(&staleRows); err != nil || staleRows != 0 {
		t.Fatalf("expired failure rows=%d err=%v after opportunistic cleanup, want 0", staleRows, err)
	}
}

func TestPasswordLoginFailureKeysUseCanonicalAccountAndIP(t *testing.T) {
	first := passwordLoginFailureKeys("canonical-user-id", "203.0.113.7")
	second := passwordLoginFailureKeys("canonical-user-id", "203.0.113.7")
	if len(first) != 2 || len(second) != 2 || first[0] != second[0] || first[1] != second[1] {
		t.Fatalf("failure keys differ for the same canonical login identity: %#v vs %#v", first, second)
	}
	if first[0].scope != "account" || first[0].key != "canonical-user-id" || first[1].scope != "ip" || first[1].key == "203.0.113.7" {
		t.Fatalf("unexpected account/IP keys: %#v", first)
	}
}

func TestPasswordLoginCooldownStartsOnFifthFailure(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS password_login_failures (
		scope TEXT NOT NULL CHECK (scope IN ('account','ip')),
		key TEXT NOT NULL,
		window_started_at TIMESTAMPTZ NOT NULL,
		failure_count SMALLINT NOT NULL CHECK (failure_count > 0),
		locked_until TIMESTAMPTZ,
		PRIMARY KEY(scope,key)
	)`); err != nil {
		t.Fatalf("create throttle table: %v", err)
	}
	repo := NewUserRepository(pool)
	accountID, ip := uuid.NewString(), "192.0.2."+fmt.Sprint(time.Now().UnixNano()%200+1)
	keys := passwordLoginFailureKeys(accountID, ip)
	t.Cleanup(func() {
		for _, key := range keys {
			_, _ = pool.Exec(context.Background(), `DELETE FROM password_login_failures WHERE scope=$1 AND key=$2`, key.scope, key.key)
		}
	})
	for attempt := 1; attempt <= passwordLoginFailureLimit; attempt++ {
		retry, recordErr := repo.RecordPasswordLoginFailure(ctx, accountID, ip)
		if recordErr != nil {
			t.Fatalf("record attempt %d: %v", attempt, recordErr)
		}
		if attempt < passwordLoginFailureLimit && retry != 0 {
			t.Fatalf("attempt %d retry_after=%d, want no cooldown before fifth failure", attempt, retry)
		}
		if attempt == passwordLoginFailureLimit && retry <= 0 {
			t.Fatalf("fifth failure retry_after=%d, want active cooldown", retry)
		}
		if attempt == passwordLoginFailureLimit && retry < int(passwordLoginFailureTTL.Seconds())-10 {
			t.Fatalf("fifth failure retry_after=%d, want approximately 900 seconds", retry)
		}
	}
	if retry, err := repo.PasswordLoginRetryAfter(ctx, accountID, ip); err != nil || retry <= 0 {
		t.Fatalf("retry_after=%d err=%v after fifth failure, want active cooldown", retry, err)
	}
}
