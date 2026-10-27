package httpapi

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/stories"
)

func TestStorySyncReturnsStoryToPreviouslyDiscoveredContactPostgres(t *testing.T) {
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

	ownerID, recipientID, outsiderID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	insertUser := func(id string) string {
		t.Helper()
		openIMID := "openim-" + id
		username := "story-audience-" + strings.ReplaceAll(id, "-", "")
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status,openim_user_id) VALUES($1,$2,'test','active',$3)`, id, username, openIMID); err != nil {
			t.Fatalf("insert test user: %v", err)
		}
		return openIMID
	}
	insertUser(ownerID)
	insertUser(recipientID)
	insertUser(outsiderID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{ownerID, recipientID, outsiderID})
	})

	users := auth.NewUserRepository(pool)
	if err := users.AddDiscoveredContacts(ctx, ownerID, []string{recipientID}); err != nil {
		t.Fatalf("save pre-existing discovered contact: %v", err)
	}
	discoveredOpenIMIDs, err := users.DiscoveredOpenIMIDs(ctx, ownerID)
	if err != nil {
		t.Fatalf("read discovered contacts: %v", err)
	}
	discoveredPlatformIDs, err := users.PlatformUserIDsByOpenIM(ctx, discoveredOpenIMIDs)
	if err != nil {
		t.Fatalf("resolve discovered contacts: %v", err)
	}
	audience := storyAudienceSet(ownerID, nil, mapValues(discoveredPlatformIDs))
	if _, ok := audience[recipientID]; !ok {
		t.Fatalf("previously discovered recipient %s missing from status audience", recipientID)
	}
	recipients := make([]string, 0, len(audience))
	for id := range audience {
		recipients = append(recipients, id)
	}
	storyRepo := stories.NewRepository(pool)
	created, err := storyRepo.Create(ctx, stories.CreateParams{
		OwnerID: ownerID, MediaType: string(stories.MediaText), ContentType: "text/plain",
		Caption: "visible to discovered contact", Text: "visible to discovered contact",
		BackgroundColor: "#FFFFFF", Visibility: string(stories.VisibilityContacts),
		ExpiresAt: time.Now().Add(time.Hour), Recipients: recipients,
	})
	if err != nil {
		t.Fatalf("create status: %v", err)
	}

	recipientFeed, err := storyRepo.Sync(ctx, recipientID, 0, nil, 100)
	if err != nil || len(recipientFeed.Events) != 1 || recipientFeed.Events[0].StoryID != created.ID || recipientFeed.Events[0].OwnerID != ownerID {
		t.Fatalf("discovered contact feed=%+v err=%v; want the new status from its owner", recipientFeed, err)
	}
	outsiderFeed, err := storyRepo.Sync(ctx, outsiderID, 0, nil, 100)
	if err != nil || len(outsiderFeed.Events) != 0 {
		t.Fatalf("outsider feed=%+v err=%v; want no status", outsiderFeed, err)
	}
}
