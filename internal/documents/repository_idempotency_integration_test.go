package documents

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReserveIdempotentPostgresReplayConflictAndRace(t *testing.T) {
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
	ownerID := uuid.NewString()
	username := "document-idempotency-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status) VALUES ($1,$2,'test','active')`, ownerID, username); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	repository := NewRepository(pool)
	policy := DefaultPolicy()
	reserve := func(idempotencyKey, requestHash string) (*Document, bool, error) {
		return repository.ReserveIdempotent(ctx, ownerID, "message-documents/"+ownerID+"/"+uuid.NewString(), "report.pdf", "application/pdf", 4, policy, idempotencyKey, requestHash)
	}
	first, created, err := reserve("stable-key", strings.Repeat("a", 64))
	if err != nil || !created {
		t.Fatalf("first reserve document=%v created=%v err=%v", first, created, err)
	}
	replay, created, err := reserve("stable-key", strings.Repeat("a", 64))
	if err != nil || created || replay.ID != first.ID || replay.StorageKey != first.StorageKey {
		t.Fatalf("replay document=%v created=%v err=%v; want original %s", replay, created, err, first.ID)
	}
	if _, _, err = reserve("stable-key", strings.Repeat("b", 64)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different request error=%v, want conflict", err)
	}
	var documentCount, ledgerCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM message_documents WHERE owner_id=$1`, ownerID).Scan(&documentCount); err != nil || documentCount != 1 {
		t.Fatalf("document count=%d err=%v", documentCount, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM message_document_upload_idempotency WHERE owner_id=$1`, ownerID).Scan(&ledgerCount); err != nil || ledgerCount != 1 {
		t.Fatalf("idempotency ledger count=%d err=%v", ledgerCount, err)
	}

	const workers = 8
	start := make(chan struct{})
	results := make(chan string, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			document, _, reserveErr := reserve("racing-key", strings.Repeat("c", 64))
			if reserveErr != nil {
				errorsFound <- reserveErr
				return
			}
			results <- document.ID
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsFound)
	for reserveErr := range errorsFound {
		t.Fatalf("concurrent reserve: %v", reserveErr)
	}
	winner := ""
	for id := range results {
		if winner == "" {
			winner = id
		} else if id != winner {
			t.Fatalf("concurrent same-key uploads diverged: %s != %s", id, winner)
		}
	}
	if winner == "" {
		t.Fatal("concurrent reserve returned no document")
	}

	if _, err = repository.MarkDeleteRequested(ctx, ownerID, first.ID); err != nil {
		t.Fatalf("tombstone original: %v", err)
	}
	if _, _, err = reserve("stable-key", strings.Repeat("a", 64)); !errors.Is(err, ErrIdempotencyGone) {
		t.Fatalf("tombstoned replay error=%v, want gone", err)
	}
}
