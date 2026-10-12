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
