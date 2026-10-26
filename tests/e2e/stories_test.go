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
	"sync"
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
	get(t, c, base+"/v1/stories/00000000-0000-0000-0000-000000000000/views", http.StatusUnauthorized)
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
	mediaURL, ok := story["media_url"].(string)
	if !ok || mediaURL == "" {
		t.Fatalf("story media_url=%#v", story["media_url"])
	}
	parsedMediaURL, err := url.Parse(mediaURL)
	if err != nil || parsedMediaURL.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("story URL is not a private presigned URL: %q err=%v", mediaURL, err)
	}
	mediaResponse, err := c.Get(mediaURL)
	if err != nil {
		t.Fatalf("download story media: %v", err)
	}
	mediaBytes, readErr := io.ReadAll(mediaResponse.Body)
	mediaResponse.Body.Close()
	if readErr != nil || mediaResponse.StatusCode != http.StatusOK || !bytes.Equal(mediaBytes, []byte("story-image")) {
		t.Fatalf("story download status=%d bytes=%q err=%v", mediaResponse.StatusCode, mediaBytes, readErr)
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
	if err = ws.WriteJSON(map[string]any{"type": "get", "global_cursor": 0, "user_cursors": map[string]int64{}, "limit": 100}); err != nil {
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
	var deleted map[string]any
	for {
		deleted = make(map[string]any)
		if err = ws.ReadJSON(&deleted); err != nil {
			t.Fatal(err)
		}
		if deleted["type"] != "stories.event" {
			break
		}
	}
	if deleted["type"] != "stories.batch" {
		t.Fatalf("delete response=%#v", deleted)
	}
	events, ok := deleted["events"].([]any)
	if !ok {
		t.Fatalf("delete response events=%#v", deleted)
	}
	foundDeleted := false
	for _, item := range events {
		event := item.(map[string]any)
		if event["story_id"] == storyID && event["type"] == "deleted" {
			foundDeleted = true
		}
	}
	if !foundDeleted {
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

func TestStoryCreateIdempotencyAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	c := &http.Client{Timeout: 30 * time.Second}
	created := postJSON(t, c, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple"}`, fmt.Sprintf("story-idem-%d", time.Now().UnixNano())), http.StatusCreated)
	access := created["access_token"].(string)
	key := fmt.Sprintf("story-draft-%d", time.Now().UnixNano())
	first, _, _ := postStoryWithKey(t, c, base, access, "image/png", []byte("same-payload"), nil, key, http.StatusCreated)
	firstStory := first["story"].(map[string]any)
	firstID := firstStory["id"].(string)
	replay, replayHeader, _ := postStoryWithKey(t, c, base, access, "image/png", []byte("same-payload"), nil, key, http.StatusCreated)
	if got := replay["story"].(map[string]any)["id"].(string); got != firstID || replayHeader != "true" {
		t.Fatalf("sequential replay story=%q header=%q, want story=%q and replay marker", got, replayHeader, firstID)
	}
	postStoryWithKey(t, c, base, access, "image/png", []byte("changed-payload"), nil, key, http.StatusConflict)

	concurrentKey := fmt.Sprintf("story-race-%d", time.Now().UnixNano())
	const attempts = 4
	type result struct {
		id  string
		err error
	}
	ids := make(chan result, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			response, _, status, err := doPostStoryWithKey(c, base, access, "image/png", []byte("racing-payload"), nil, concurrentKey)
			if err != nil {
				ids <- result{err: err}
				return
			}
			if status != http.StatusCreated {
				ids <- result{err: fmt.Errorf("status=%d, want=%d", status, http.StatusCreated)}
				return
			}
			ids <- result{id: response["story"].(map[string]any)["id"].(string)}
		}()
	}
	group.Wait()
	close(ids)
	winner := ""
	for response := range ids {
		if response.err != nil {
			t.Fatal(response.err)
		}
		if winner == "" {
			winner = response.id
		} else if response.id != winner {
			t.Fatalf("concurrent same-key creates returned different stories: %q and %q", winner, response.id)
		}
	}
	deleteStory(t, c, base, access, firstID, http.StatusOK)
	deleteStory(t, c, base, access, winner, http.StatusOK)
	postStoryWithKey(t, c, base, access, "image/png", []byte("same-payload"), nil, key, http.StatusGone)
}

func TestStoryLiveFanoutAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	c := &http.Client{Timeout: 30 * time.Second}
	password := "correct-horse-battery-staple"
	sender := postJSON(t, c, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, fmt.Sprintf("story-live-sender-%d", time.Now().UnixNano()), password), http.StatusCreated)
	receiver := postJSON(t, c, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, fmt.Sprintf("story-live-receiver-%d", time.Now().UnixNano()), password), http.StatusCreated)
	senderAccess, receiverAccess := sender["access_token"].(string), receiver["access_token"].(string)
	senderOpenIMID, receiverOpenIMID := openIMUserID(t, sender), openIMUserID(t, receiver)
	senderOpenIMToken := issueDeviceOpenIMTokenForE2E(t, c, base, senderAccess)
	receiverOpenIMToken := issueDeviceOpenIMTokenForE2E(t, c, base, receiverAccess)
	testOpenIMFriendInteroperability(t, c, base, senderOpenIMToken, receiverOpenIMToken, senderOpenIMID, receiverOpenIMID)
	sendOpenIMStoryProbe(t, base, senderOpenIMToken, senderOpenIMID, receiverOpenIMID)

	receiverWS := dialStoryWS(t, base, receiverAccess)
	defer receiverWS.Close()
	created := postStory(t, c, base, senderAccess, "image/png", []byte("live-story-image"), nil, http.StatusCreated)
	storyID := created["story"].(map[string]any)["id"].(string)
	var live struct {
		Type  string `json:"type"`
		Event struct {
			EventID int64  `json:"event_id"`
			Type    string `json:"type"`
			StoryID string `json:"story_id"`
			Story   struct {
				MediaURL string `json:"media_url"`
			} `json:"story"`
		} `json:"event"`
	}
	if err := receiverWS.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := receiverWS.ReadJSON(&live); err != nil {
		t.Fatal(err)
	}
	if live.Type != "stories.event" || live.Event.Type != "created" || live.Event.StoryID != storyID || live.Event.EventID == 0 {
		t.Fatalf("live create event=%#v", live)
	}
	if live.Event.Story.MediaURL == "" {
		t.Fatalf("live story event did not include a signed media URL: %#v", live)
	}

	postAuthenticatedJSON(t, c, base+"/v1/stories/"+storyID+"/view", receiverAccess, `{"reaction":"🔥"}`, http.StatusOK)
	views := getAuthenticatedJSON(t, c, base+"/v1/stories/"+storyID+"/views", senderAccess, http.StatusOK)
	if len(views["views"].([]any)) != 1 {
		t.Fatalf("viewed-by=%#v", views)
	}
	deleteStory(t, c, base, senderAccess, storyID, http.StatusOK)
	if err := receiverWS.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := receiverWS.ReadJSON(&live); err != nil {
		t.Fatal(err)
	}
	if live.Type != "stories.event" || live.Event.Type != "deleted" || live.Event.StoryID != storyID {
		t.Fatalf("live delete event=%#v", live)
	}
}

func TestStoryRetainedRowLimitAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	c := &http.Client{Timeout: 30 * time.Second}
	created := postJSON(t, c, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple"}`, fmt.Sprintf("story-retained-limit-%d", time.Now().UnixNano())), http.StatusCreated)
	access := created["access_token"].(string)
	for i := 0; i < 40; i++ {
		story := postStory(t, c, base, access, "video/mp4", []byte("retained"), nil, http.StatusCreated)
		deleteStory(t, c, base, access, story["story"].(map[string]any)["id"].(string), http.StatusOK)
	}
	postStory(t, c, base, access, "image/png", []byte("forty-first"), nil, http.StatusTooManyRequests)
}

func sendOpenIMStoryProbe(t *testing.T, base, token, senderID, receiverID string) {
	t.Helper()
	conn := dialOpenIMClient(t, base, token, senderID)
	defer conn.Close()
	operationID := fmt.Sprintf("story-live-probe-%d", time.Now().UnixNano())
	payload, err := json.Marshal(map[string]any{
		"reqIdentifier": 1003,
		"token":         token,
		"sendID":        senderID,
		"operationID":   operationID,
		"msgIncr":       senderID + "_" + operationID,
		"data":          encodeE2EMessageData(senderID, receiverID, "story-live-probe"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatal(err)
	}
	reply := readOpenIMJSONFrame(t, conn, 20*time.Second)
	if reply["reqIdentifier"] != float64(1003) || reply["errCode"] != float64(0) {
		t.Fatalf("story OpenIM probe failed: %#v", reply)
	}
}

func postStory(t *testing.T, c *http.Client, base, access, contentType string, media []byte, fields map[string]string, want int) map[string]any {
	result, _, _ := postStoryWithKey(t, c, base, access, contentType, media, fields, "", want)
	return result
}

func postStoryWithKey(t *testing.T, c *http.Client, base, access, contentType string, media []byte, fields map[string]string, key string, want int) (map[string]any, string, int) {
	t.Helper()
	result, replayHeader, status, err := doPostStoryWithKey(c, base, access, contentType, media, fields, key)
	if err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("story upload status=%d want=%d", status, want)
	}
	return result, replayHeader, status
}

func doPostStoryWithKey(c *http.Client, base, access, contentType string, media []byte, fields map[string]string, key string) (map[string]any, string, int, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="media"; filename="story.bin"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", 0, err
	}
	if _, err = part.Write(media); err != nil {
		return nil, "", 0, err
	}
	for key, value := range fields {
		if err = writer.WriteField(key, value); err != nil {
			return nil, "", 0, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, "", 0, err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/stories", &body)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, resp.Header.Get("Idempotency-Replayed"), resp.StatusCode, nil
	}
	var result map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", resp.StatusCode, err
	}
	return result, resp.Header.Get("Idempotency-Replayed"), resp.StatusCode, nil
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
