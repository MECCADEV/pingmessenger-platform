package channels

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestChannelSearchVisibilityAndPinnedPostsPostgres(t *testing.T) {
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
	username := "channel-test-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status) VALUES($1,$2,'test','active')`, ownerID, username); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })
	repo := NewRepository(pool)
	public, err := repo.Create(ctx, CreateParams{OpenIMGroupID: "ch_" + strings.ReplaceAll(uuid.NewString(), "-", ""), CreatedBy: ownerID, Name: "Channel Public Alpha", Description: "discoverable term", PhotoURL: "https://cdn.example/channel.png", Visibility: VisibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	private, err := repo.Create(ctx, CreateParams{OpenIMGroupID: "ch_" + strings.ReplaceAll(uuid.NewString(), "-", ""), CreatedBy: ownerID, Name: "Channel Private Alpha", Visibility: VisibilityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	results, total, err := repo.SearchPublic(ctx, "Alpha", 20, 0)
	if err != nil || total != 1 || len(results) != 1 || results[0].ID != public.ID {
		t.Fatalf("public search results=%+v total=%d err=%v", results, total, err)
	}
	if got, err := repo.ByID(ctx, private.ID); err != nil || got.Visibility != VisibilityPrivate {
		t.Fatalf("private channel lookup=%+v err=%v", got, err)
	}
	if _, err = repo.Create(ctx, CreateParams{OpenIMGroupID: public.OpenIMGroupID, CreatedBy: ownerID, Name: "duplicate", Visibility: VisibilityPublic}); err == nil {
		t.Fatal("duplicate OpenIM group mapping unexpectedly succeeded")
	}
	pin, created, err := repo.Pin(ctx, public.ID, "message-1", ownerID)
	if err != nil || !created || pin.MessageID != "message-1" {
		t.Fatalf("Pin() = %+v created=%v err=%v", pin, created, err)
	}
	replay, created, err := repo.Pin(ctx, public.ID, "message-1", ownerID)
	if err != nil || created || replay.PinnedAt.IsZero() {
		t.Fatalf("duplicate Pin() = %+v created=%v err=%v", replay, created, err)
	}
	pins, err := repo.Pins(ctx, public.ID, 10)
	if err != nil || len(pins) != 1 || pins[0].MessageID != "message-1" {
		t.Fatalf("Pins() = %+v err=%v", pins, err)
	}
	removed, err := repo.Unpin(ctx, public.ID, "message-1")
	if err != nil || !removed {
		t.Fatalf("Unpin() = %v, %v", removed, err)
	}
	removed, err = repo.Unpin(ctx, public.ID, "message-1")
	if err != nil || removed {
		t.Fatalf("second Unpin() = %v, %v", removed, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM channel_pinned_posts WHERE channel_id=$1`, public.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("pin count = %d, err=%v", count, err)
	}
	if _, err = repo.ByID(ctx, uuid.NewString()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing channel error=%v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO channels(openim_group_id,created_by,name,visibility) VALUES($1,$2,$3,'public')`, "gp_"+fmt.Sprint(time.Now().UnixNano()), ownerID, "not a channel"); err == nil {
		t.Fatal("non-channel group prefix unexpectedly passed the database guard")
	}
}
