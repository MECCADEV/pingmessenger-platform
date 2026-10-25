package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/documents"
	"pingmessenger/internal/ratelimit"
)

type documentIdempotencyObjectStore struct {
	mu          sync.Mutex
	objects     map[string][]byte
	putKeys     []string
	failNextPut bool
}

func (s *documentIdempotencyObjectStore) Put(_ context.Context, key, _ string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putKeys = append(s.putKeys, key)
	if s.failNextPut {
		s.failNextPut = false
		return errors.New("injected object-store failure")
	}
	if s.objects == nil {
		s.objects = make(map[string][]byte)
	}
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

func (s *documentIdempotencyObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *documentIdempotencyObjectStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://private.example/" + key, nil
}

func (s *documentIdempotencyObjectStore) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := append([]string(nil), s.putKeys...)
	return len(s.objects), keys
}

func TestUploadDocumentHTTPIdempotencyReplayConflictAndObjectRetry(t *testing.T) {
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

	ownerID, sessionID := uuid.NewString(), uuid.NewString()
	username := "document-http-idempotency-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,openim_user_id,status) VALUES ($1,$2,'test',$3,'active')`, ownerID, username, "openim-"+ownerID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO device_sessions(id,user_id,refresh_token_hash,platform_id,expires_at) VALUES ($1,$2,$3,'test',now()+interval '1 hour')`, sessionID, ownerID, uuid.NewString()); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	tokens := auth.NewTokenManager(strings.Repeat("d", 40), "document-test", time.Hour, time.Hour)
	accessToken, _, err := tokens.IssueAccess(ownerID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	objects := &documentIdempotencyObjectStore{}
	api := &API{
		users:           auth.NewUserRepository(pool),
		tokens:          tokens,
		documents:       documents.NewRepository(pool),
		documentObjects: objects,
		documentPolicy:  documents.DefaultPolicy(),
		limiter:         ratelimit.NewLocal(),
	}
	upload := func(key, filename, contentType string, data []byte) (int, map[string]any) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="document"; filename="%s"`, filename))
		header.Set("Content-Type", contentType)
		part, createErr := writer.CreatePart(header)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := part.Write(data); writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr := writer.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		request := &fasthttp.RequestCtx{}
		request.Request.SetRequestURI("/v1/documents")
		request.Request.Header.SetMethod(fasthttp.MethodPost)
		request.Request.Header.SetContentType(writer.FormDataContentType())
		request.Request.Header.Set("Authorization", "Bearer "+accessToken)
		request.Request.Header.Set("Idempotency-Key", key)
		request.Request.SetBody(body.Bytes())
		api.uploadDocument(request)
		result := make(map[string]any)
		if request.Response.StatusCode() == fasthttp.StatusCreated || request.Response.StatusCode() == fasthttp.StatusOK {
			if decodeErr := json.Unmarshal(request.Response.Body(), &result); decodeErr != nil {
				t.Fatalf("decode upload response: %v", decodeErr)
			}
		}
		return request.Response.StatusCode(), result
	}
	resultDocumentID := func(result map[string]any) string {
		t.Helper()
		doc, ok := result["document"].(map[string]any)
		if !ok {
			t.Fatalf("missing document in response: %#v", result)
		}
		id, _ := doc["id"].(string)
		return id
	}

	status, first := upload("same-key", "report.pdf", "application/pdf", []byte("content"))
	if status != fasthttp.StatusCreated {
		t.Fatalf("first upload status=%d", status)
	}
	firstID := resultDocumentID(first)
	status, replay := upload("same-key", "report.pdf", "application/pdf", []byte("content"))
	if status != fasthttp.StatusOK || resultDocumentID(replay) != firstID {
		t.Fatalf("matching replay status=%d response=%#v, want original %s", status, replay, firstID)
	}
	if status, _ = upload("same-key", "report.pdf", "application/pdf", []byte("changed")); status != fasthttp.StatusConflict {
		t.Fatalf("changed-content replay status=%d, want 409", status)
	}
	var documentCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM message_documents WHERE owner_id=$1`, ownerID).Scan(&documentCount); err != nil || documentCount != 1 {
		t.Fatalf("document rows=%d err=%v, want one", documentCount, err)
	}
	objectCount, putKeys := objects.snapshot()
	if objectCount != 1 || len(putKeys) != 2 || putKeys[0] != putKeys[1] {
		t.Fatalf("exact replay must target one canonical object: objects=%d puts=%v", objectCount, putKeys)
	}

	objects.mu.Lock()
	objects.failNextPut = true
	objects.mu.Unlock()
	if status, _ = upload("retry-put-key", "retry.pdf", "application/pdf", []byte("retry")); status != fasthttp.StatusBadGateway {
		t.Fatalf("injected object-store error status=%d, want 502", status)
	}
	status, recovered := upload("retry-put-key", "retry.pdf", "application/pdf", []byte("retry"))
	if status != fasthttp.StatusOK {
		t.Fatalf("exact retry after object-store error status=%d, want 200", status)
	}
	recoveredID := resultDocumentID(recovered)
	var recoveredCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM message_documents WHERE owner_id=$1 AND id=$2`, ownerID, recoveredID).Scan(&recoveredCount); err != nil || recoveredCount != 1 {
		t.Fatalf("retried reservation rows=%d err=%v, want one", recoveredCount, err)
	}
}
