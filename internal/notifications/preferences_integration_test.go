package notifications

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPreferencesRepositoryPostgresIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("notification_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("connect to isolated schema: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE users (
		id UUID PRIMARY KEY, openim_user_id TEXT UNIQUE, status TEXT NOT NULL,
		deleted_at TIMESTAMPTZ
	)`); err != nil {
		t.Fatalf("create isolated users table: %v", err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0027_notification_preferences.sql"))
	if err != nil {
		t.Fatalf("read notification migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply notification migration: %v", err)
	}
	const activeID = "10000000-0000-4000-8000-000000000001"
	const inactiveID = "10000000-0000-4000-8000-000000000002"
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,openim_user_id,status,deleted_at) VALUES
		($1,'openim-active','active',NULL),($2,'openim-deleted','active',now())`, activeID, inactiveID); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	repository := NewRepository(pool)
	settings, err := repository.GetGlobal(ctx, activeID)
	if err != nil || settings != Defaults() {
		t.Fatalf("global defaults = %+v, %v", settings, err)
	}

	// Two overlapping partial updates must merge atomically instead of replacing
	// the whole row with whichever request finishes last.
	mode, sound := "mentions_only", "silent"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for _, input := range []Input{{Mode: &mode}, {Sound: &sound}} {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, updateErr := repository.PutGlobal(ctx, activeID, input)
			errs <- updateErr
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for updateErr := range errs {
		if updateErr != nil {
			t.Fatalf("concurrent global update: %v", updateErr)
		}
	}
	settings, err = repository.GetGlobal(ctx, activeID)
	if err != nil || settings.Mode != mode || settings.Sound != sound {
		t.Fatalf("concurrent global updates lost a field: %+v, %v", settings, err)
	}

	conversation := "group-1"
	frequency := 10
	start = make(chan struct{})
	errs = make(chan error, 2)
	wait = sync.WaitGroup{}
	for _, input := range []Input{{Enabled: boolPointer(false)}, {MaxUpdateFrequencySeconds: &frequency}} {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, updateErr := repository.PutConversation(ctx, activeID, conversation, input)
			errs <- updateErr
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for updateErr := range errs {
		if updateErr != nil {
			t.Fatalf("concurrent conversation update: %v", updateErr)
		}
	}
	settings, err = repository.GetConversation(ctx, activeID, conversation)
	if err != nil || settings.Enabled || settings.MaxUpdateFrequencySeconds != frequency || settings.Mode != mode || settings.Sound != sound {
		t.Fatalf("conversation override did not inherit global values or preserve concurrent updates: %+v, %v", settings, err)
	}

	resolved, err := repository.Resolve(ctx, []string{"openim-active", "openim-deleted", "unknown"}, conversation)
	if err != nil {
		t.Fatalf("resolve preferences: %v", err)
	}
	if len(resolved) != 1 || resolved["openim-active"] != settings {
		t.Fatalf("resolved preferences = %+v, want only active user's effective settings %+v", resolved, settings)
	}
	if err := repository.DeleteConversation(ctx, activeID, conversation); err != nil {
		t.Fatalf("delete conversation overrides: %v", err)
	}
	settings, err = repository.GetConversation(ctx, activeID, conversation)
	if err != nil || settings != (Settings{Enabled: true, Mode: mode, Sound: sound, MaxUpdateFrequencySeconds: 2}) {
		t.Fatalf("reset conversation settings = %+v, %v", settings, err)
	}
}

func boolPointer(value bool) *bool { return &value }
