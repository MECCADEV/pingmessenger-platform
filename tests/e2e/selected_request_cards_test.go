//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestSelectedRequestCardsAgainstDeployedAPI covers the six cards shipped in
// API v0.11.0. It deliberately deletes the primary test account last so a
// failed intermediate assertion leaves useful diagnostic state.
func TestSelectedRequestCardsAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	password := "correct-horse-battery-staple"
	newPassword := "correct-horse-battery-staple-rotated"
	primaryName := fmt.Sprintf("selected-cards-%d", time.Now().UnixNano())
	primary := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, primaryName, password), http.StatusCreated)
	primaryAccess := primary["access_token"].(string)

	postAuthenticatedJSON(t, client, base+"/v1/auth/password/change", primaryAccess, fmt.Sprintf(`{"current_password":%q,"new_password":%q}`, password, newPassword), http.StatusOK)
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, primaryName, password), http.StatusUnauthorized, "old password after change")
	rotated := postJSON(t, client, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, primaryName, newPassword), http.StatusOK)
	if rotated["access_token"] == "" || rotated["refresh_token"] == "" {
		t.Fatalf("rotated login did not return tokens: %#v", rotated)
	}
	// The current session remains valid after the password change.
	getAuthenticatedJSON(t, client, base+"/v1/profile/", primaryAccess, http.StatusOK)

	uploadImage(t, client, base, primaryAccess)
	deleteProfileImage(t, client, base, primaryAccess)
	profile := getAuthenticatedJSON(t, client, base+"/v1/profile/", primaryAccess, http.StatusOK)
	if image, present := profile["image"]; present && image != nil && image != "" {
		t.Fatalf("profile image still present after deletion: %#v", profile)
	}

	textStory := postTextStory(t, client, base, primaryAccess, "hello from v0.11.0", "#8619F5", http.StatusCreated)
	textPayload := textStory["story"].(map[string]any)
	if textPayload["media_type"] != "text" || textPayload["text"] != "hello from v0.11.0" || textPayload["background_color"] != "#8619F5" || textPayload["media_url"] != "" {
		t.Fatalf("unexpected text status payload: %#v", textPayload)
	}
	textStoryID := textPayload["id"].(string)
	postAuthenticatedJSON(t, client, base+"/v1/stories/"+textStoryID+"/view", primaryAccess, `{"reaction":""}`, http.StatusOK)
	views := getAuthenticatedJSON(t, client, base+"/v1/stories/"+textStoryID+"/views", primaryAccess, http.StatusOK)
	if len(views["views"].([]any)) != 1 {
		t.Fatalf("text status viewed-by response: %#v", views)
	}
	deleteStory(t, client, base, primaryAccess, textStoryID, http.StatusOK)

	peerName := fmt.Sprintf("selected-peer-%d", time.Now().UnixNano())
	peer := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q,"nickname":"Selected peer"}`, peerName, password), http.StatusCreated)
	primaryOpenIMID, peerOpenIMID := openIMUserID(t, primary), openIMUserID(t, peer)
	primaryOpenIMToken := issueDeviceOpenIMTokenForE2E(t, client, base, primaryAccess)
	peerOpenIMToken := issueDeviceOpenIMTokenForE2E(t, client, base, peer["access_token"].(string))
	testOpenIMFriendInteroperability(t, client, base, primaryOpenIMToken, peerOpenIMToken, primaryOpenIMID, peerOpenIMID)
	friends := getAuthenticatedJSON(t, client, base+"/v1/friends", primaryAccess, http.StatusOK)
	friendItems, ok := friends["friends"].([]any)
	if !ok || !containsPingID(friendItems, peer["ping_id"].(string)) {
		t.Fatalf("friends response did not contain peer: %#v", friends)
	}

	postAuthenticatedJSON(t, client, base+"/v1/blocks", primaryAccess, fmt.Sprintf(`{"ping_id":%q}`, peer["ping_id"]), http.StatusCreated)
	blocks := getAuthenticatedJSON(t, client, base+"/v1/blocks", primaryAccess, http.StatusOK)
	if !containsPingID(blocks["blocks"].([]any), peer["ping_id"].(string)) {
		t.Fatalf("blocks response did not contain peer: %#v", blocks)
	}
	deleteNoContent(t, client, base+"/v1/blocks/"+peer["ping_id"].(string), primaryAccess)

	deleted := postAuthenticatedJSON(t, client, base+"/v1/account/delete", primaryAccess, fmt.Sprintf(`{"current_password":%q,"accept_terms":true}`, newPassword), http.StatusOK)
	if deleted["status"] != "deleted" {
		t.Fatalf("account deletion response: %#v", deleted)
	}
	if deleted["deleted_at"] == nil || deleted["deleted_at"] == "" {
		t.Fatalf("soft-delete response lacks deleted_at: %#v", deleted)
	}
	assertStatus(t, client, http.MethodGet, base+"/v1/profile/", "", http.StatusUnauthorized, "deleted account bearer token")
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, primaryName, newPassword), http.StatusUnauthorized, "deleted account username login")
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/login", fmt.Sprintf(`{"ping_id":%q,"password":%q,"platform_id":"web"}`, primary["ping_id"], newPassword), http.StatusUnauthorized, "deleted account PingID login")
	available := postJSON(t, client, base+"/v1/auth/verify-username", fmt.Sprintf(`{"username":%q}`, primaryName), http.StatusOK)
	if available["available"] != false {
		t.Fatalf("deleted account username was reported available: %#v", available)
	}
	// The identity remains reserved, while the account's data is retained.
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, primaryName, password), http.StatusConflict, "deleted account username reuse")
}

func containsPingID(items []any, pingID string) bool {
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if ok && item["ping_id"] == pingID {
			return true
		}
	}
	return false
}

func deleteProfileImage(t *testing.T, c *http.Client, base, access string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, base+"/v1/profile/image", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("profile image delete status=%d body=%s", resp.StatusCode, detail)
	}
}

func deleteNoContent(t *testing.T, c *http.Client, endpoint, access string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("DELETE %s status=%d body=%s", endpoint, resp.StatusCode, detail)
	}
}

func postTextStory(t *testing.T, c *http.Client, base, access, text, background string, want int) map[string]any {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"type": "text", "text": text, "background_color": background, "visibility": "contacts"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
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
		t.Fatalf("text story status=%d want=%d body=%s", resp.StatusCode, want, detail)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
