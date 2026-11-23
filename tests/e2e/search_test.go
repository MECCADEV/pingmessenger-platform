//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUserDirectorySearchAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	prefix := fmt.Sprintf("search-%d", time.Now().UnixNano())
	ownerName := prefix + "-owner"
	peerName := prefix + "-alice"
	password := "correct-horse-battery-staple"
	owner := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, ownerName, password), http.StatusCreated)
	peer := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q,"nickname":"Alice Directory"}`, peerName, password), http.StatusCreated)
	if owner["openim_sync"] != "complete" || peer["openim_sync"] != "complete" {
		t.Fatalf("directory search requires OpenIM-provisioned accounts: owner=%#v peer=%#v", owner["openim_sync"], peer["openim_sync"])
	}
	ownerAccess := owner["access_token"].(string)
	assertStatus(t, client, http.MethodPost, base+"/v1/friends/search", fmt.Sprintf(`{"query":%q,"field":"username"}`, strings.ToUpper(prefix+"-ali")), http.StatusUnauthorized, "unauthenticated search")

	partial := postAuthenticatedJSON(t, client, base+"/v1/friends/search", ownerAccess, fmt.Sprintf(`{"query":%q,"field":"username","limit":20}`, strings.ToUpper(prefix)), http.StatusOK)
	results, ok := partial["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("partial username search results=%#v", partial)
	}
	item, ok := results[0].(map[string]any)
	if !ok || item["username"] != peerName || item["nickname"] != "Alice Directory" || item["relationship"] != float64(-1) {
		t.Fatalf("unexpected OpenIM-shaped search result=%#v", item)
	}
	for _, key := range []string{"userID", "nickname", "faceURL", "remark", "relationship", "username", "ping_id"} {
		if _, exists := item[key]; !exists {
			t.Fatalf("search result missing %q: %#v", key, item)
		}
	}

	byPingID := postAuthenticatedJSON(t, client, base+"/v1/friends/search", ownerAccess, fmt.Sprintf(`{"query":%q,"field":"ping_id"}`, peer["ping_id"]), http.StatusOK)
	if total, ok := byPingID["total"].(float64); !ok || total != 1 {
		t.Fatalf("exact ping_id search response=%#v", byPingID)
	}
	postAuthenticatedJSON(t, client, base+"/v1/friends/search", ownerAccess, `{"query":"alice","field":"unsupported"}`, http.StatusUnprocessableEntity)
}

func TestBlockedUserVisibilityAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	prefix := fmt.Sprintf("blocked-status-%d", time.Now().UnixNano())
	password := "correct-horse-battery-staple"
	viewer := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, prefix+"-viewer", password), http.StatusCreated)
	blocker := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q,"nickname":"Blocker"}`, prefix+"-blocker", password), http.StatusCreated)
	viewerAccess := viewer["access_token"].(string)
	blockerAccess := blocker["access_token"].(string)

	postAuthenticatedJSON(t, client, base+"/v1/blocks", blockerAccess, fmt.Sprintf(`{"ping_id":%q}`, viewer["ping_id"]), http.StatusCreated)
	storyResponse := postStory(t, client, base, blockerAccess, "image/png", []byte("blocked-viewer-story"), map[string]string{
		"visibility": "allow", "user_ids": fmt.Sprintf(`["%s"]`, viewer["user_id"]),
	}, http.StatusCreated)
	storyID := storyResponse["story"].(map[string]any)["id"].(string)
	status := getAuthenticatedJSON(t, client, base+"/v1/users/"+blocker["ping_id"].(string)+"/status", viewerAccess, http.StatusForbidden)
	if status["code"] != "BLOCKED_BY_USER" {
		t.Fatalf("blocked status response=%#v", status)
	}

	broad := postAuthenticatedJSON(t, client, base+"/v1/friends/search", viewerAccess, fmt.Sprintf(`{"query":%q,"field":"username"}`, prefix+"-block"), http.StatusOK)
	if broad["total"] != float64(0) {
		t.Fatalf("broad username search returned blocker: %#v", broad)
	}
	exact := postAuthenticatedJSON(t, client, base+"/v1/friends/search", viewerAccess, fmt.Sprintf(`{"query":%q,"field":"ping_id"}`, blocker["ping_id"]), http.StatusOK)
	items := exact["results"].([]any)
	if len(items) != 1 {
		t.Fatalf("exact PingID search lost the match: %#v", exact)
	}
	item := items[0].(map[string]any)
	if item["status_code"] != "BLOCKED_BY_USER" || item["faceURL"] != nil {
		t.Fatalf("exact search did not return the block status with the image redacted: %#v", item)
	}
	media := getAuthenticatedJSON(t, client, base+"/v1/stories/"+storyID+"/media", viewerAccess, http.StatusNotFound)
	if media["error"] == nil {
		t.Fatalf("blocked viewer received story media: %#v", media)
	}
	view := postAuthenticatedJSON(t, client, base+"/v1/stories/"+storyID+"/view", viewerAccess, `{}`, http.StatusForbidden)
	if view["code"] != "BLOCKED_BY_USER" {
		t.Fatalf("blocked viewer story response=%#v", view)
	}
	ws := dialStoryWS(t, base, viewerAccess)
	if err := ws.WriteJSON(map[string]any{"type": "sync", "global_cursor": 0, "user_cursors": map[string]int64{}, "limit": 100}); err != nil {
		t.Fatal(err)
	}
	var storyBatch map[string]any
	if err := ws.ReadJSON(&storyBatch); err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	if events, ok := storyBatch["events"].([]any); ok {
		for _, raw := range events {
			if event, ok := raw.(map[string]any); ok && event["story_id"] == storyID {
				t.Fatalf("blocked viewer received story event: %#v", event)
			}
		}
	}

	// The target cannot remove another user's block through their own unblock API.
	deleteNoContent(t, client, base+"/v1/blocks/"+blocker["ping_id"].(string), viewerAccess)
	status = getAuthenticatedJSON(t, client, base+"/v1/users/"+blocker["ping_id"].(string)+"/status", viewerAccess, http.StatusForbidden)
	if status["code"] != "BLOCKED_BY_USER" {
		t.Fatalf("caller removed an incoming block: %#v", status)
	}

	// The block owner can remove their own block; exact and broad search then
	// return the normal profile state again.
	deleteNoContent(t, client, base+"/v1/blocks/"+viewer["ping_id"].(string), blockerAccess)
	status = getAuthenticatedJSON(t, client, base+"/v1/users/"+blocker["ping_id"].(string)+"/status", viewerAccess, http.StatusOK)
	if status["status_code"] != "AVAILABLE" {
		t.Fatalf("status after owner unblock=%#v", status)
	}
	postAuthenticatedJSON(t, client, base+"/v1/stories/"+storyID+"/view", viewerAccess, `{}`, http.StatusOK)
	broad = postAuthenticatedJSON(t, client, base+"/v1/friends/search", viewerAccess, fmt.Sprintf(`{"query":%q,"field":"username"}`, prefix+"-block"), http.StatusOK)
	if broad["total"] != float64(1) {
		t.Fatalf("broad search did not restore the unblocked account: %#v", broad)
	}
}
