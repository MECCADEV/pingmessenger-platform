package reports

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateListAndResolveReport(t *testing.T) {
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
	reporterID, targetID := uuid.NewString(), uuid.NewString()
	insert := func(id string) {
		t.Helper()
		username := "report-test-" + strings.ReplaceAll(id, "-", "")
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status,openim_user_id) VALUES($1,$2,'test','active',$3)`, id, username, "im-"+id); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}
	insert(reporterID)
	insert(targetID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM moderation_reports WHERE reporter_user_id=ANY($1::uuid[]) OR target_user_id=ANY($1::uuid[])`, []string{reporterID, targetID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{reporterID, targetID})
	})

	repo := NewRepository(pool)
	conversationID, messageID, seq := "conv-report-test", "msg-report-test", int64(12)
	snapshot := json.RawMessage(`{"clientMsgID":"msg-report-test","sendID":"im-target"}`)
	input := CreateInput{ReporterUserID: reporterID, TargetUserID: targetID, ConversationID: &conversationID, MessageID: &messageID, MessageSeq: &seq, Reason: "abuse", Details: "review this", MessageSnapshot: snapshot}
	reportID, err := repo.Create(ctx, input)
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	if _, err = repo.Create(ctx, input); err == nil {
		t.Fatal("duplicate report unexpectedly accepted")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Fatalf("duplicate error=%v", err)
		}
	}
	items, err := repo.List(ctx, "open", 100, 0)
	if err != nil {
		t.Fatalf("list reports=%d err=%v", len(items), err)
	}
	var found *Record
	for i := range items {
		if items[i].ID == reportID {
			found = &items[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("report %s missing from %d open reports", reportID, len(items))
	}
	var actualSnapshot, expectedSnapshot map[string]any
	if err = json.Unmarshal(found.MessageSnapshot, &actualSnapshot); err != nil {
		t.Fatalf("decode stored snapshot: %v", err)
	}
	if err = json.Unmarshal(snapshot, &expectedSnapshot); err != nil {
		t.Fatal(err)
	}
	if found.MessageSeq == nil || *found.MessageSeq != seq || actualSnapshot["clientMsgID"] != expectedSnapshot["clientMsgID"] || actualSnapshot["sendID"] != expectedSnapshot["sendID"] {
		t.Fatalf("listed report=%+v", found)
	}
	if err := repo.UpdateStatus(ctx, reportID, reporterID, "", "actioned", "reviewed"); err != nil {
		t.Fatal(err)
	}
	items, err = repo.List(ctx, "actioned", 100, 0)
	found = nil
	for i := range items {
		if items[i].ID == reportID {
			found = &items[i]
			break
		}
	}
	if err != nil || found == nil || found.ResolutionNotes != "reviewed" || found.ReviewedBy == nil || *found.ReviewedBy != reporterID {
		t.Fatalf("resolved report=%+v err=%v", items, err)
	}
}
