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

func TestViewIdempotentPostgresReplayConflictAndConcurrentAttempts(t *testing.T) {
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
	username := "story-view-idempotency-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status) VALUES ($1,$2,'test','active')`, ownerID, username); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	story, created, err := repository.CreateIdempotent(ctx, CreateParams{
		OwnerID: ownerID, MediaType: "text", ContentType: "text/plain", Caption: "view target", Text: "view target",
		BackgroundColor: "#FFFFFF", Visibility: "contacts", ExpiresAt: time.Now().Add(time.Hour),
		Recipients: []string{ownerID}, IdempotencyKey: "view-target-story", RequestHash: strings.Repeat("a", 64),
	})
	if err != nil || !created {
		t.Fatalf("create target story=%v created=%v err=%v", story, created, err)
	}

	first, replayed, err := repository.ViewIdempotent(ctx, ownerID, story.ID, "heart", "open-1", strings.Repeat("b", 64))
	if err != nil || replayed || first == nil || first.Reaction != "heart" {
		t.Fatalf("first view=%v replayed=%v err=%v", first, replayed, err)
	}
	duplicate, replayed, err := repository.ViewIdempotent(ctx, ownerID, story.ID, "heart", "open-1", strings.Repeat("b", 64))
	if err != nil || !replayed || duplicate.ViewedAt != first.ViewedAt || duplicate.Reaction != "heart" {
		t.Fatalf("replay view=%v replayed=%v err=%v; want original snapshot %v", duplicate, replayed, err, first)
	}
	if _, _, err = repository.ViewIdempotent(ctx, ownerID, story.ID, "fire", "open-1", strings.Repeat("c", 64)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed request error=%v, want conflict", err)
	}
	current, replayed, err := repository.ViewIdempotent(ctx, ownerID, story.ID, "fire", "open-2", strings.Repeat("d", 64))
	if err != nil || replayed || current == nil || current.Reaction != "fire" {
		t.Fatalf("later explicit view=%v replayed=%v err=%v", current, replayed, err)
	}
	oldReplay, replayed, err := repository.ViewIdempotent(ctx, ownerID, story.ID, "heart", "open-1", strings.Repeat("b", 64))
	if err != nil || !replayed || oldReplay.ViewedAt != first.ViewedAt {
		t.Fatalf("old retry=%v replayed=%v err=%v; want original snapshot", oldReplay, replayed, err)
	}
	var currentReaction string
	if err = pool.QueryRow(ctx, `SELECT reaction FROM story_views WHERE story_id=$1 AND viewer_id=$2`, story.ID, ownerID).Scan(&currentReaction); err != nil || currentReaction != "fire" {
		t.Fatalf("current reaction=%q err=%v; replay must not revert later state", currentReaction, err)
	}

	const workers = 6
	start := make(chan struct{})
	results := make(chan *View, workers)
	replays := make(chan bool, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			view, wasReplay, viewErr := repository.ViewIdempotent(ctx, ownerID, story.ID, "wave", "concurrent-open", strings.Repeat("e", 64))
			if viewErr != nil {
				errorsFound <- viewErr
				return
			}
			results <- view
			replays <- wasReplay
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(replays)
	close(errorsFound)
	for viewErr := range errorsFound {
		t.Fatalf("concurrent view: %v", viewErr)
	}
	winnerAt := time.Time{}
	freshCount := 0
	for view := range results {
		if winnerAt.IsZero() {
			winnerAt = view.ViewedAt
		} else if !view.ViewedAt.Equal(winnerAt) {
			t.Fatalf("concurrent views diverged: %v != %v", view.ViewedAt, winnerAt)
		}
	}
	for wasReplay := range replays {
		if !wasReplay {
			freshCount++
		}
	}
	if freshCount != 1 {
		t.Fatalf("fresh concurrent operations=%d, want 1", freshCount)
	}
	if _, err = pool.Exec(ctx, `UPDATE story_view_idempotency SET retain_until=now()-interval '1 second' WHERE viewer_id=$1 AND idempotency_key='open-1'`, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, replayed, err = repository.ViewIdempotent(ctx, ownerID, story.ID, "heart", "open-1", strings.Repeat("b", 64)); err != nil || replayed {
		t.Fatalf("expired key reuse replayed=%v err=%v; want a fresh operation", replayed, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM stories WHERE id=$1`, story.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.ViewIdempotent(ctx, ownerID, story.ID, "heart", "open-2", strings.Repeat("d", 64)); !errors.Is(err, ErrIdempotencyGone) {
		t.Fatalf("deleted story replay error=%v, want gone", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE story_view_idempotency SET retain_until=now()-interval '1 second' WHERE viewer_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	if removed, cleanupErr := repository.CleanupExpiredIdempotency(ctx, 500); cleanupErr != nil || removed == 0 {
		t.Fatalf("expired idempotency cleanup removed=%d err=%v", removed, cleanupErr)
	}
}
