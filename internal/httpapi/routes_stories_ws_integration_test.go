package httpapi

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/ratelimit"
	"pingmessenger/internal/stories"
)

func TestStoryWebSocketReconnectReplayAndCursorRegression(t *testing.T) {
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
	username := "story-ws-" + strings.ReplaceAll(ownerID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status) VALUES ($1,$2,'test','active')`, ownerID, username); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO device_sessions(id,user_id,refresh_token_hash,platform_id,expires_at) VALUES ($1,$2,$3,'test',now()+interval '1 hour')`, sessionID, ownerID, uuid.NewString()); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID) })

	tokens := auth.NewTokenManager(strings.Repeat("w", 40), "story-ws-test", time.Hour, time.Hour)
	accessToken, _, err := tokens.IssueAccess(ownerID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	api := &API{
		users:   auth.NewUserRepository(pool),
		tokens:  tokens,
		stories: stories.NewRepository(pool),
		limiter: ratelimit.NewLocal(),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fasthttp.Server{Handler: api.Router()}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Shutdown()
		<-serveDone
	})

	create := func(caption string) {
		t.Helper()
		_, createErr := api.stories.Create(ctx, stories.CreateParams{
			OwnerID: ownerID, MediaType: string(stories.MediaText), ContentType: "text/plain",
			Caption: caption, Text: caption, BackgroundColor: "#FFFFFF",
			Visibility: string(stories.VisibilityContacts), ExpiresAt: time.Now().Add(time.Hour),
			Recipients: []string{ownerID},
		})
		if createErr != nil {
			t.Fatalf("create story %q: %v", caption, createErr)
		}
	}
	create("first")
	create("second")

	url := "ws://" + listener.Addr().String() + "/v1/stories/ws"
	connect := func() *websocket.Conn {
		t.Helper()
		header := http.Header{"Authorization": []string{"Bearer " + accessToken}}
		conn, response, dialErr := websocket.DefaultDialer.DialContext(ctx, url, header)
		if dialErr != nil {
			if response != nil {
				t.Fatalf("dial story WebSocket: %v (HTTP %d)", dialErr, response.StatusCode)
			}
			t.Fatalf("dial story WebSocket: %v", dialErr)
		}
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	readBatch := func(conn *websocket.Conn) storySyncMessage {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var batch struct {
			Type         string           `json:"type"`
			Events       []stories.Event  `json:"events"`
			GlobalCursor int64            `json:"global_cursor"`
			UserCursors  map[string]int64 `json:"user_cursors"`
			HasMore      bool             `json:"has_more"`
		}
		if err := conn.ReadJSON(&batch); err != nil {
			t.Fatalf("read story sync batch: %v", err)
		}
		return storySyncMessage{Type: batch.Type, GlobalCursor: batch.GlobalCursor, UserCursors: batch.UserCursors, Limit: len(batch.Events)}
	}
	send := func(conn *websocket.Conn, message storySyncMessage) {
		t.Helper()
		if err := conn.WriteJSON(message); err != nil {
			t.Fatalf("send story WebSocket request: %v", err)
		}
	}

	firstConn := connect()
	send(firstConn, storySyncMessage{Type: "sync", Limit: 1})
	firstPage := readBatch(firstConn)
	if firstPage.Type != "stories.batch" || firstPage.Limit != 1 || firstPage.GlobalCursor == 0 {
		t.Fatalf("first sync response=%+v; want one event and cursor", firstPage)
	}
	if err := firstConn.Close(); err != nil {
		t.Fatalf("disconnect before ACK: %v", err)
	}

	// A reconnect with the last locally committed cursor must replay the
	// unacknowledged event instead of skipping it.
	reconnect := connect()
	defer reconnect.Close()
	send(reconnect, storySyncMessage{Type: "sync", Limit: 1})
	replayed := readBatch(reconnect)
	if replayed.Type != "stories.batch" || replayed.GlobalCursor != firstPage.GlobalCursor || replayed.Limit != firstPage.Limit {
		t.Fatalf("reconnect response=%+v; want the same page as %+v", replayed, firstPage)
	}

	send(reconnect, storySyncMessage{Type: "ack", GlobalCursor: firstPage.GlobalCursor, UserCursors: map[string]int64{ownerID: firstPage.GlobalCursor}})
	var ack struct {
		Type string `json:"type"`
	}
	if err := reconnect.ReadJSON(&ack); err != nil || ack.Type != "stories.ack" {
		t.Fatalf("first-page ACK response=%+v err=%v", ack, err)
	}

	send(reconnect, storySyncMessage{Type: "ack", GlobalCursor: firstPage.GlobalCursor - 1})
	var regression struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if err := reconnect.ReadJSON(&regression); err != nil || regression.Type != "error" || regression.Code != "cursor_regression" {
		t.Fatalf("regressing ACK response=%+v err=%v; want cursor_regression", regression, err)
	}

	send(reconnect, storySyncMessage{Type: "sync", GlobalCursor: firstPage.GlobalCursor, UserCursors: map[string]int64{ownerID: firstPage.GlobalCursor}, Limit: 1})
	secondPage := readBatch(reconnect)
	if secondPage.Type != "stories.batch" || secondPage.Limit != 1 || secondPage.GlobalCursor <= firstPage.GlobalCursor {
		t.Fatalf("second page=%+v; want the next event after the acknowledged cursor", secondPage)
	}
}
