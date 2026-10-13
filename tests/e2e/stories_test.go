//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
)

func TestStoryContractAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	c := &http.Client{Timeout: 30 * time.Second}
	created := postJSON(t, c, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple"}`, fmt.Sprintf("story-contract-%d", time.Now().UnixNano())), http.StatusCreated)
	access := created["access_token"].(string)

	postStory(t, c, base, access, "application/octet-stream", []byte("not-media"), nil, http.StatusUnprocessableEntity)
	postStory(t, c, base, access, "image/png", bytes.Repeat([]byte("x"), 10<<20+1), nil, http.StatusRequestEntityTooLarge)
	postStory(t, c, base, access, "video/mp4", bytes.Repeat([]byte("x"), 10<<20+1), nil, http.StatusRequestEntityTooLarge)
	postStory(t, c, base, access, "image/png", []byte("bad-visibility"), map[string]string{"visibility": "public"}, http.StatusUnprocessableEntity)

	before := time.Now()
	createdStory := postStory(t, c, base, access, "image/png", []byte("story-image"), map[string]string{"caption": "hello 🌈", "visibility": "contacts"}, http.StatusCreated)
	story := createdStory["story"].(map[string]any)
	storyID := story["id"].(string)
	if story["media_type"] != "image" || story["content_type"] != "image/png" || story["caption"] != "hello 🌈" || story["visibility"] != "contacts" {
		t.Fatalf("story metadata = %#v", story)
	}
	if got := int64(story["byte_size"].(float64)); got != int64(len("story-image")) {
		t.Fatalf("byte_size=%d", got)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, story["expires_at"].(string))
	if err != nil || expiresAt.Before(before.Add(23*time.Hour+50*time.Minute)) || expiresAt.After(time.Now().Add(24*time.Hour+10*time.Minute)) {
		t.Fatalf("default expires_at=%v err=%v", story["expires_at"], err)
	}

	short := postStory(t, c, base, access, "video/mp4", []byte("video"), map[string]string{"expires_in_seconds": "60"}, http.StatusCreated)
	shortStory := short["story"].(map[string]any)
	shortID := shortStory["id"].(string)
	shortExpiry, err := time.Parse(time.RFC3339Nano, shortStory["expires_at"].(string))
	if err != nil || shortExpiry.Before(time.Now().Add(50*time.Second)) || shortExpiry.After(time.Now().Add(70*time.Second)) {
		t.Fatalf("custom expires_at=%v err=%v", shortStory["expires_at"], err)
	}
	postStory(t, c, base, access, "image/png", []byte("bad-expiry"), map[string]string{"expires_in_seconds": "59"}, http.StatusUnprocessableEntity)

	postAuthenticatedJSON(t, c, base+"/v1/stories/"+storyID+"/view", access, `{"reaction":"🔥"}`, http.StatusOK)
	postAuthenticatedJSON(t, c, base+"/v1/stories/"+storyID+"/view", access, `{"reaction":"❤️"}`, http.StatusOK)
	postAuthenticatedJSON(t, c, base+"/v1/stories/"+storyID+"/view", access, fmt.Sprintf(`{"reaction":%q}`, strings.Repeat("x", 65)), http.StatusUnprocessableEntity)
	views := getAuthenticatedJSON(t, c, base+"/v1/stories/"+storyID+"/views", access, http.StatusOK)
	items, ok := views["views"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("views=%#v", views["views"])
	}
	view := items[len(items)-1].(map[string]any)
	if view["user_id"] != created["user_id"] || view["reaction"] != "❤️" {
		t.Fatalf("viewed-by record=%#v", view)
	}
	if _, err := time.Parse(time.RFC3339Nano, view["viewed_at"].(string)); err != nil {
		t.Fatalf("viewed_at=%v: %v", view["viewed_at"], err)
	}

	ws := dialStoryWS(t, base, access)
	defer ws.Close()
	if err = ws.WriteJSON(map[string]any{"type": "sync", "global_cursor": 0, "user_cursors": map[string]int64{}, "limit": 100}); err != nil {
		t.Fatal(err)
	}
	var batch struct {
		Type   string `json:"type"`
		Events []struct {
			EventID int64  `json:"event_id"`
			Type    string `json:"type"`
			StoryID string `json:"story_id"`
		} `json:"events"`
		GlobalCursor int64 `json:"global_cursor"`
	}
	if err = ws.ReadJSON(&batch); err != nil {
		t.Fatal(err)
	}
	if batch.Type != "stories.batch" || batch.GlobalCursor == 0 {
		t.Fatalf("initial batch=%#v", batch)
	}
	foundCreated := false
	for _, event := range batch.Events {
		if event.StoryID == storyID && event.Type == "created" {
			foundCreated = true
		}
	}
	if !foundCreated {
		t.Fatalf("created story absent from batch: %#v", batch.Events)
	}
	if err = ws.WriteJSON(map[string]any{"type": "ack", "global_cursor": batch.GlobalCursor, "user_cursors": map[string]int64{created["user_id"].(string): batch.GlobalCursor}}); err != nil {
		t.Fatal(err)
	}
	var ack map[string]any
	if err = ws.ReadJSON(&ack); err != nil || ack["type"] != "stories.ack" {
		t.Fatalf("ack=%#v err=%v", ack, err)
	}
	if err = ws.WriteJSON(map[string]any{"type": "sync", "global_cursor": 0, "user_cursors": map[string]int64{}, "limit": 100}); err != nil {
		t.Fatal(err)
	}
	var regression map[string]any
	if err = ws.ReadJSON(&regression); err != nil || regression["code"] != "cursor_regression" {
		t.Fatalf("regression=%#v err=%v", regression, err)
	}

	deletedResponse := deleteStory(t, c, base, access, storyID, http.StatusOK)
	deletedStory, ok := deletedResponse["story"].(map[string]any)
	if !ok || deletedStory["deleted_at"] == nil {
		t.Fatalf("delete response lacks deleted_at: %#v", deletedResponse)
	}
	if err = ws.WriteJSON(map[string]any{"type": "sync", "global_cursor": batch.GlobalCursor, "user_cursors": map[string]int64{created["user_id"].(string): batch.GlobalCursor}, "limit": 100}); err != nil {
		t.Fatal(err)
	}
	var deleted struct {
		Type   string `json:"type"`
		Events []struct {
			Type    string `json:"type"`
			StoryID string `json:"story_id"`
		} `json:"events"`
	}
	if err = ws.ReadJSON(&deleted); err != nil {
		t.Fatal(err)
	}
	foundDeleted := false
	for _, event := range deleted.Events {
		if event.StoryID == storyID && event.Type == "deleted" {
			foundDeleted = true
		}
	}
	if deleted.Type != "stories.batch" || !foundDeleted {
		t.Fatalf("delete batch=%#v", deleted)
	}
	deleteStory(t, c, base, access, shortID, http.StatusOK)

	activeIDs := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		active := postStory(t, c, base, access, "image/png", []byte("active"), nil, http.StatusCreated)
		activeIDs = append(activeIDs, active["story"].(map[string]any)["id"].(string))
	}
	postStory(t, c, base, access, "video/mp4", []byte("eleventh"), nil, http.StatusTooManyRequests)
	for _, id := range activeIDs {
		deleteStory(t, c, base, access, id, http.StatusOK)
	}
}

func postStory(t *testing.T, c *http.Client, base, access, contentType string, media []byte, fields map[string]string, want int) map[string]any {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="media"; filename="story.bin"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(media); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if err = writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/stories", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("story upload status=%d want=%d content_type=%s bytes=%d body=%s", resp.StatusCode, want, contentType, len(media), detail)
	}
	if want != http.StatusCreated {
		return nil
	}
	var result map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func deleteStory(t *testing.T, c *http.Client, base, access, storyID string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, base+"/v1/stories/"+storyID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("story delete status=%d want=%d", resp.StatusCode, want)
	}
	var result map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func getAuthenticatedJSON(t *testing.T, c *http.Client, endpoint, access string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s status=%d want=%d", endpoint, resp.StatusCode, want)
	}
	var result map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func dialStoryWS(t *testing.T, base, access string) *websocket.Conn {
	t.Helper()
	u, err := url.Parse(base + "/v1/stories/ws")
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": []string{"Bearer " + access}})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestProfileUploadLimitAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	c := &http.Client{Timeout: 30 * time.Second}
	created := postJSON(t, c, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple"}`, fmt.Sprintf("profile-limit-%d", time.Now().UnixNano())), http.StatusCreated)
	postProfileImage(t, c, base, created["access_token"].(string), bytes.Repeat([]byte("x"), 5<<20+1), http.StatusRequestEntityTooLarge)
}

func postProfileImage(t *testing.T, c *http.Client, base, access string, image []byte, want int) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="avatar.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(image)
	_ = writer.Close()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/profile/update", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("profile upload status=%d want=%d bytes=%d body=%s", resp.StatusCode, want, len(image), detail)
	}
}
