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

func TestSyncReplayAndAckCursorMonotonicityPostgres(t *testing.T) {
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
	username := "story-sync-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status) VALUES ($1,$2,'test','active')`, ownerID, username); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	create := func(caption string) {
		t.Helper()
		_, err := repository.Create(ctx, CreateParams{
			OwnerID: ownerID, MediaType: string(MediaText), ContentType: "text/plain", Caption: caption,
			Text: caption, BackgroundColor: "#FFFFFF", Visibility: string(VisibilityContacts),
			ExpiresAt: time.Now().Add(time.Hour), Recipients: []string{ownerID},
		})
		if err != nil {
			t.Fatalf("create story %q: %v", caption, err)
		}
	}
	create("first")
	create("second")

	// A disconnect before ACK must be recoverable from the client's last
	// committed cursor. The unacknowledged page is therefore replayed exactly.
	first, err := repository.Sync(ctx, ownerID, 0, nil, 1)
	if err != nil || len(first.Events) != 1 || !first.HasMore {
		t.Fatalf("first page=%+v err=%v; want one event and another page", first, err)
	}
	replayed, err := repository.Sync(ctx, ownerID, 0, nil, 1)
	if err != nil || len(replayed.Events) != 1 || replayed.Events[0].EventID != first.Events[0].EventID {
		t.Fatalf("reconnect replay=%+v err=%v; want event %d", replayed, err, first.Events[0].EventID)
	}

	if err = repository.Ack(ctx, ownerID, first.GlobalCursor, first.UserCursors); err != nil {
		t.Fatalf("ack committed first page: %v", err)
	}
	if err = repository.Ack(ctx, ownerID, first.GlobalCursor-1, nil); !errors.Is(err, ErrCursorRegression) {
		t.Fatalf("regressing ACK error=%v; want ErrCursorRegression", err)
	}
	if _, err = repository.Sync(ctx, ownerID, first.GlobalCursor-1, nil, 1); !errors.Is(err, ErrCursorRegression) {
		t.Fatalf("regressing sync error=%v; want ErrCursorRegression", err)
	}

	second, err := repository.Sync(ctx, ownerID, first.GlobalCursor, first.UserCursors, 1)
	if err != nil || len(second.Events) != 1 || second.Events[0].EventID <= first.GlobalCursor || second.HasMore {
		t.Fatalf("next page=%+v err=%v; want the next event only", second, err)
	}
	if err = repository.Ack(ctx, ownerID, second.GlobalCursor, second.UserCursors); err != nil {
		t.Fatalf("ack committed second page: %v", err)
	}

	create("third")
	create("fourth")
	finalPage, err := repository.Sync(ctx, ownerID, second.GlobalCursor, second.UserCursors, 2)
	if err != nil || len(finalPage.Events) != 2 {
		t.Fatalf("concurrent ACK fixture=%+v err=%v; want two events", finalPage, err)
	}
	lowCursor := finalPage.Events[0].EventID
	highCursor := finalPage.Events[1].EventID
	const racingACKs = 20
	start := make(chan struct{})
	type ackResult struct {
		cursor int64
		err    error
	}
	results := make(chan ackResult, racingACKs*2)
	var workers sync.WaitGroup
	for range racingACKs {
		for _, cursor := range []int64{lowCursor, highCursor} {
			workers.Add(1)
			go func(cursor int64) {
				defer workers.Done()
				<-start
				results <- ackResult{cursor: cursor, err: repository.Ack(ctx, ownerID, cursor, map[string]int64{ownerID: cursor})}
			}(cursor)
		}
	}
	close(start)
	workers.Wait()
	close(results)
	highestACKSucceeded := false
	for result := range results {
		if result.cursor == highCursor && result.err == nil {
			highestACKSucceeded = true
		} else if result.err != nil && !errors.Is(result.err, ErrCursorRegression) {
			t.Fatalf("concurrent ACK returned unexpected error: %v", result.err)
		}
	}
	if !highestACKSucceeded {
		t.Fatal("no concurrent ACK committed the highest cursor")
	}
	var persistedGlobal, persistedOwner int64
	if err = pool.QueryRow(ctx, `SELECT cursor FROM story_sync_cursors WHERE user_id=$1 AND scope_key='global'`, ownerID).Scan(&persistedGlobal); err != nil {
		t.Fatalf("load persisted global cursor: %v", err)
	}
	if err = pool.QueryRow(ctx, `SELECT cursor FROM story_sync_cursors WHERE user_id=$1 AND scope_key=$2`, ownerID, ownerID).Scan(&persistedOwner); err != nil {
		t.Fatalf("load persisted owner cursor: %v", err)
	}
	if persistedGlobal != highCursor || persistedOwner != highCursor {
		t.Fatalf("concurrent ACKs left global=%d owner=%d; want both=%d", persistedGlobal, persistedOwner, highCursor)
	}
}
