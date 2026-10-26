package stories

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateIdempotentPostgresRacesAndReplay(t *testing.T) {
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
	repository := NewRepository(pool)
	ownerID := uuid.NewString()
	username := "story-idempotency-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status) VALUES ($1,$2,'test','active')`, ownerID, username); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	params := func(key, hash string) CreateParams {
		return CreateParams{
			OwnerID: ownerID, MediaType: "text", ContentType: "text/plain", Caption: "hello", Text: "hello",
			BackgroundColor: "#FFFFFF", Visibility: "contacts", ExpiresAt: time.Now().Add(time.Hour),
			Recipients: []string{ownerID}, IdempotencyKey: key, RequestHash: hash,
		}
	}

	first, created, err := repository.CreateIdempotent(ctx, params("same-key", strings.Repeat("a", 64)))
	if err != nil || !created {
		t.Fatalf("first create story=%v created=%v err=%v", first, created, err)
	}
	replay, created, err := repository.CreateIdempotent(ctx, params("same-key", strings.Repeat("a", 64)))
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("replay story=%v created=%v err=%v; want original %s", replay, created, err, first.ID)
	}
	if _, _, err = repository.CreateIdempotent(ctx, params("same-key", strings.Repeat("b", 64))); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed request error=%v, want conflict", err)
	}
	var storyCount, eventCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM story_create_idempotency WHERE owner_id=$1 AND idempotency_key='same-key'`, ownerID).Scan(&storyCount); err != nil || storyCount != 1 {
		t.Fatalf("idempotency ledger count=%d err=%v", storyCount, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM story_events WHERE story_id=$1 AND event_type='created'`, first.ID).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("story creation events=%d err=%v", eventCount, err)
	}

	const workers = 6
	start := make(chan struct{})
	results := make(chan *Story, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			story, _, createErr := repository.CreateIdempotent(ctx, params("racing-key", strings.Repeat("c", 64)))
			if createErr != nil {
				errorsFound <- createErr
				return
			}
			results <- story
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsFound)
	for createErr := range errorsFound {
		t.Fatalf("concurrent create: %v", createErr)
	}
	winner := ""
	for story := range results {
		if winner == "" {
			winner = story.ID
		} else if story.ID != winner {
			t.Fatalf("concurrent same-key creates diverged: %s != %s", story.ID, winner)
		}
	}
	if winner == "" {
		t.Fatal("concurrent create returned no story")
	}

	rollbackParams := params("rollback-key", strings.Repeat("e", 64))
	rollbackParams.Recipients = []string{"not-a-uuid"}
	if _, _, err = repository.CreateIdempotent(ctx, rollbackParams); err == nil {
		t.Fatal("invalid recipient should roll the create transaction back")
	}
	rollbackParams.Recipients = []string{ownerID}
	if retried, wasCreated, retryErr := repository.CreateIdempotent(ctx, rollbackParams); retryErr != nil || !wasCreated {
		t.Fatalf("retry after transaction rollback story=%v created=%v err=%v", retried, wasCreated, retryErr)
	}

	if _, err = pool.Exec(ctx, `DELETE FROM stories WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.CreateIdempotent(ctx, params("same-key", strings.Repeat("a", 64))); !errors.Is(err, ErrIdempotencyGone) {
		t.Fatalf("deleted result replay error=%v, want gone", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE story_create_idempotency SET retain_until=now()-interval '1 second' WHERE owner_id=$1 AND idempotency_key='same-key'`, ownerID); err != nil {
		t.Fatal(err)
	}
	newResult, created, err := repository.CreateIdempotent(ctx, params("same-key", strings.Repeat("d", 64)))
	if err != nil || !created || newResult.ID == first.ID {
		t.Fatalf("expired key reuse story=%v created=%v err=%v", newResult, created, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE story_create_idempotency SET retain_until=now()-interval '1 second' WHERE owner_id=$1 AND idempotency_key='same-key'`, ownerID); err != nil {
		t.Fatal(err)
	}
	removed, err := repository.CleanupExpiredIdempotency(ctx, 500)
	if err != nil || removed == 0 {
		t.Fatalf("expired ledger cleanup removed=%d err=%v", removed, err)
	}
}
