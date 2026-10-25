package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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
	"pingmessenger/internal/ratelimit"
	"pingmessenger/internal/stories"
)

type idempotencyTestObjectStore struct {
	mu       sync.Mutex
	objects  map[string][]byte
	puts     int
	deletes  int
	presigns int
}

func (s *idempotencyTestObjectStore) Put(_ context.Context, key, _ string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects == nil {
		s.objects = make(map[string][]byte)
	}
	s.puts++
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

func (s *idempotencyTestObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	s.deletes++
	return nil
}

func (s *idempotencyTestObjectStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.presigns++
	return "https://private.example/" + key, nil
}

func (s *idempotencyTestObjectStore) counts() (putAttempts, retainedObjects, deletes, presigns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts, len(s.objects), s.deletes, s.presigns
}

func (s *idempotencyTestObjectStore) contains(key string, want []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Equal(s.objects[key], want)
}

func TestCreateStoryHTTPIdempotencyConcurrentReplay(t *testing.T) {
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
	sessionID := uuid.NewString()
	username := "story-http-idempotency-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,openim_user_id,status) VALUES ($1,$2,'test',$3,'active')`, ownerID, username, "openim-"+ownerID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO device_sessions(id,user_id,refresh_token_hash,platform_id,expires_at) VALUES ($1,$2,$3,'test',now()+interval '1 hour')`, sessionID, ownerID, uuid.NewString()); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	tokens := auth.NewTokenManager(strings.Repeat("s", 40), "story-test", time.Hour, time.Hour)
	accessToken, _, err := tokens.IssueAccess(ownerID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	api := &API{
		users:      auth.NewUserRepository(pool),
		tokens:     tokens,
		stories:    stories.NewRepository(pool),
		limiter:    ratelimit.NewLocal(),
		storyGraph: map[string]map[string]struct{}{ownerID: {}},
	}

	const requests = 6
	start := make(chan struct{})
	responses := make(chan *fasthttp.RequestCtx, requests)
	errorsFound := make(chan error, requests)
	var workers sync.WaitGroup
	for range requests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			request, requestErr := textStoryRequest(accessToken, "same-http-key", "same story")
			if requestErr != nil {
				errorsFound <- requestErr
				return
			}
			<-start
			api.createStory(request)
			responses <- request
		}()
	}
	close(start)
	workers.Wait()
	close(responses)
	close(errorsFound)
	for requestErr := range errorsFound {
		t.Fatal(requestErr)
	}

	var canonicalStoryID string
	replayed := 0
	for response := range responses {
		if response.Response.StatusCode() != fasthttp.StatusCreated {
			t.Fatalf("same-key HTTP create returned %d: %s", response.Response.StatusCode(), response.Response.Body())
		}
		var body struct {
			Story stories.Story `json:"story"`
		}
		if err := json.Unmarshal(response.Response.Body(), &body); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		if canonicalStoryID == "" {
			canonicalStoryID = body.Story.ID
		} else if body.Story.ID != canonicalStoryID {
			t.Fatalf("same-key requests created stories %s and %s", canonicalStoryID, body.Story.ID)
		}
		if string(response.Response.Header.Peek("Idempotency-Replayed")) == "true" {
			replayed++
		}
	}
	if canonicalStoryID == "" || replayed != requests-1 {
		t.Fatalf("canonical story=%q replay headers=%d; want one winner and %d replays", canonicalStoryID, replayed, requests-1)
	}
	var storiesCreated, createEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stories WHERE owner_id=$1`, ownerID).Scan(&storiesCreated); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM story_events WHERE story_id=$1 AND event_type='created'`, canonicalStoryID).Scan(&createEvents); err != nil {
		t.Fatal(err)
	}
	if storiesCreated != 1 || createEvents != 1 {
		t.Fatalf("same-key request created %d stories and %d create events", storiesCreated, createEvents)
	}

	conflict, err := textStoryRequest(accessToken, "same-http-key", "different story")
	if err != nil {
		t.Fatal(err)
	}
	api.createStory(conflict)
	if conflict.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("same key with changed body status=%d body=%s; want 409", conflict.Response.StatusCode(), conflict.Response.Body())
	}
}

func TestCreateMediaStoryHTTPIdempotencyConcurrentReplayCleansLosingUploads(t *testing.T) {
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
	sessionID := uuid.NewString()
	username := "story-media-idempotency-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,openim_user_id,status) VALUES ($1,$2,'test',$3,'active')`, ownerID, username, "openim-"+ownerID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO device_sessions(id,user_id,refresh_token_hash,platform_id,expires_at) VALUES ($1,$2,$3,'test',now()+interval '1 hour')`, sessionID, ownerID, uuid.NewString()); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	tokens := auth.NewTokenManager(strings.Repeat("m", 40), "story-media-test", time.Hour, time.Hour)
	accessToken, _, err := tokens.IssueAccess(ownerID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	objects := &idempotencyTestObjectStore{objects: make(map[string][]byte)}
	api := &API{
		users:      auth.NewUserRepository(pool),
		tokens:     tokens,
		stories:    stories.NewRepository(pool),
		objects:    objects,
		limiter:    ratelimit.NewLocal(),
		storyGraph: map[string]map[string]struct{}{ownerID: {}},
	}

	const requests = 6
	start := make(chan struct{})
	responses := make(chan *fasthttp.RequestCtx, requests)
	errorsFound := make(chan error, requests)
	var workers sync.WaitGroup
	for range requests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			request, requestErr := mediaStoryRequest(accessToken, "same-media-key", []byte("same image bytes"))
			if requestErr != nil {
				errorsFound <- requestErr
				return
			}
			<-start
			api.createStory(request)
			responses <- request
		}()
	}
	close(start)
	workers.Wait()
	close(responses)
	close(errorsFound)
	for requestErr := range errorsFound {
		t.Fatal(requestErr)
	}

	var canonicalStoryID, canonicalMediaURL string
	replayed := 0
	for response := range responses {
		if response.Response.StatusCode() != fasthttp.StatusCreated {
			t.Fatalf("same-key media create returned %d: %s", response.Response.StatusCode(), response.Response.Body())
		}
		var body struct {
			Story stories.Story `json:"story"`
		}
		if err := json.Unmarshal(response.Response.Body(), &body); err != nil {
			t.Fatalf("decode media create response: %v", err)
		}
		if body.Story.MediaURL == "" {
			t.Fatalf("media replay omitted private media fields: %+v", body.Story)
		}
		if canonicalStoryID == "" {
			canonicalStoryID, canonicalMediaURL = body.Story.ID, body.Story.MediaURL
		} else if body.Story.ID != canonicalStoryID || body.Story.MediaURL != canonicalMediaURL {
			t.Fatalf("same-key media requests diverged: first=(%s,%s) next=(%s,%s)", canonicalStoryID, canonicalMediaURL, body.Story.ID, body.Story.MediaURL)
		}
		if string(response.Response.Header.Peek("Idempotency-Replayed")) == "true" {
			replayed++
		}
	}
	if replayed != requests-1 {
		t.Fatalf("media replay headers=%d; want %d", replayed, requests-1)
	}
	var canonicalStorageKey string
	if err := pool.QueryRow(ctx, `SELECT storage_key FROM stories WHERE id=$1`, canonicalStoryID).Scan(&canonicalStorageKey); err != nil {
		t.Fatal(err)
	}
	if !objects.contains(canonicalStorageKey, []byte("same image bytes")) {
		t.Fatalf("canonical story storage key %q does not match the retained media object", canonicalStorageKey)
	}
	putAttempts, retainedObjects, deletes, presigns := objects.counts()
	if putAttempts < 1 || putAttempts > requests || retainedObjects != 1 || deletes != putAttempts-1 || presigns != putAttempts {
		t.Fatalf("media object state puts=%d retained=%d deletes=%d presigns=%d; want one canonical object and cleanup/signing for each upload", putAttempts, retainedObjects, deletes, presigns)
	}

	retry, err := mediaStoryRequest(accessToken, "same-media-key", []byte("same image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	api.createStory(retry)
	if retry.Response.StatusCode() != fasthttp.StatusCreated || string(retry.Response.Header.Peek("Idempotency-Replayed")) != "true" {
		t.Fatalf("sequential media replay status=%d replay=%q body=%s", retry.Response.StatusCode(), retry.Response.Header.Peek("Idempotency-Replayed"), retry.Response.Body())
	}
	if afterPuts, afterRetained, afterDeletes, afterPresigns := objects.counts(); afterPuts != putAttempts || afterRetained != retainedObjects || afterDeletes != deletes || afterPresigns != presigns {
		t.Fatalf("sequential media replay changed object state puts=%d retained=%d deletes=%d presigns=%d", afterPuts, afterRetained, afterDeletes, afterPresigns)
	}

	conflict, err := mediaStoryRequest(accessToken, "same-media-key", []byte("different image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	api.createStory(conflict)
	if conflict.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("changed media under same key status=%d body=%s; want 409", conflict.Response.StatusCode(), conflict.Response.Body())
	}
	if afterPuts, afterRetained, afterDeletes, afterPresigns := objects.counts(); afterPuts != putAttempts || afterRetained != retainedObjects || afterDeletes != deletes || afterPresigns != presigns {
		t.Fatalf("conflicting replay touched object store puts=%d retained=%d deletes=%d presigns=%d", afterPuts, afterRetained, afterDeletes, afterPresigns)
	}
}

func textStoryRequest(accessToken, key, text string) (*fasthttp.RequestCtx, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"type":             "text",
		"text":             text,
		"background_color": "#123456",
	} {
		if err := writer.WriteField(name, value); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	request := new(fasthttp.RequestCtx)
	request.Request.SetRequestURI("/v1/stories")
	request.Request.Header.SetMethod(fasthttp.MethodPost)
	request.Request.Header.SetContentType(writer.FormDataContentType())
	request.Request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Request.Header.Set("Idempotency-Key", key)
	request.Request.Header.Set("X-Forwarded-For", "192.0.2.10")
	request.Request.SetBody(body.Bytes())
	return request, nil
}

func mediaStoryRequest(accessToken, key string, data []byte) (*fasthttp.RequestCtx, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("type", "image"); err != nil {
		return nil, err
	}
	mediaHeader := make(textproto.MIMEHeader)
	mediaHeader.Set("Content-Disposition", `form-data; name="media"; filename="story.png"`)
	mediaHeader.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(mediaHeader)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	request := new(fasthttp.RequestCtx)
	request.Request.SetRequestURI("/v1/stories")
	request.Request.Header.SetMethod(fasthttp.MethodPost)
	request.Request.Header.SetContentType(writer.FormDataContentType())
	request.Request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Request.Header.Set("Idempotency-Key", key)
	request.Request.Header.Set("X-Forwarded-For", "192.0.2.11")
	request.Request.SetBody(body.Bytes())
	return request, nil
}
