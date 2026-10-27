package auth

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiscoveredContactsAreAdditiveAndExplicitlyClearable(t *testing.T) {
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

	ownerID, firstID, secondID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	insertUser := func(id, openIMID string) {
		t.Helper()
		username := "discovered-" + strings.ReplaceAll(id, "-", "")
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status,openim_user_id) VALUES($1,$2,'test','active',$3)`, id, username, openIMID); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}
	insertUser(ownerID, "openim-"+ownerID)
	insertUser(firstID, "openim-"+firstID)
	insertUser(secondID, "openim-"+secondID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{ownerID, firstID, secondID})
	})

	repository := NewUserRepository(pool)
	if err := repository.AddDiscoveredContacts(ctx, ownerID, []string{firstID}); err != nil {
		t.Fatalf("add first discovered contact: %v", err)
	}
	if err := repository.AddDiscoveredContacts(ctx, ownerID, []string{firstID, secondID}); err != nil {
		t.Fatalf("add additional discovered contacts: %v", err)
	}
	ids, err := repository.DiscoveredOpenIMIDs(ctx, ownerID)
	if err != nil {
		t.Fatalf("list discovered contacts: %v", err)
	}
	want := map[string]bool{"openim-" + firstID: true, "openim-" + secondID: true}
	if len(ids) != len(want) {
		t.Fatalf("listed %v, want two additive contacts", ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected discovered contact %q", id)
		}
	}
	if err := repository.ClearDiscoveredContacts(ctx, ownerID); err != nil {
		t.Fatalf("clear discovered contacts: %v", err)
	}
	ids, err = repository.DiscoveredOpenIMIDs(ctx, ownerID)
	if err != nil || len(ids) != 0 {
		t.Fatalf("after clear list=%v err=%v, want empty", ids, err)
	}
}
