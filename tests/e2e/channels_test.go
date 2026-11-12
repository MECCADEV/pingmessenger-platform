//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestChannelsAgainstDeployedAPI(t *testing.T) {
	base := strings.TrimRight(os.Getenv("E2E_BASE_URL"), "/")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	password := "correct-horse-battery-staple"
	assertStatus(t, client, http.MethodPost, base+"/v1/channels", `{"name":"unauthenticated","visibility":"public"}`, http.StatusUnauthorized, "unauthenticated channel create")
	ownerName := fmt.Sprintf("channel-owner-%d", time.Now().UnixNano())
	owner := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, ownerName, password), http.StatusCreated)
	if owner["openim_sync"] != "complete" {
		t.Fatalf("channel owner OpenIM provisioning=%v", owner["openim_sync"])
	}
	access := owner["access_token"].(string)
	assertStatus(t, client, http.MethodGet, base+"/v1/channels/search", "", http.StatusUnauthorized, "unauthenticated channel search")
	assertStatus(t, client, http.MethodGet, base+"/v1/channels/00000000-0000-4000-8000-000000000001", "", http.StatusUnauthorized, "unauthenticated channel detail")
	assertStatus(t, client, http.MethodGet, base+"/v1/channels/00000000-0000-4000-8000-000000000001/pins", "", http.StatusUnauthorized, "unauthenticated pin list")
	assertStatus(t, client, http.MethodPost, base+"/v1/channels/00000000-0000-4000-8000-000000000001/pins", `{"message_id":"unauthenticated"}`, http.StatusUnauthorized, "unauthenticated pin create")
	assertStatus(t, client, http.MethodDelete, base+"/v1/channels/00000000-0000-4000-8000-000000000001/pins/unauthenticated", "", http.StatusUnauthorized, "unauthenticated pin delete")

	create := func(name, visibility string) map[string]any {
		t.Helper()
		return postAuthenticatedJSON(t, client, base+"/v1/channels", access, fmt.Sprintf(`{"name":%q,"description":"channel E2E","photo_url":"https://cdn.example.test/channel.png","visibility":%q}`, name, visibility), http.StatusCreated)
	}
	public := create("channel-public-"+ownerName, "public")
	publicChannel := public["channel"].(map[string]any)
	publicID, _ := publicChannel["id"].(string)
	publicGroupID, _ := publicChannel["openim_group_id"].(string)
	if publicID == "" || !strings.HasPrefix(publicGroupID, "ch_") || publicChannel["member_count"] != float64(1) {
		t.Fatalf("public channel create response=%#v", public)
	}

	search := func(query string) []any {
		t.Helper()
		result := getChannelJSON(t, client, base+"/v1/channels/search?q="+url.QueryEscape(query), access, http.StatusOK)
		channels, ok := result["channels"].([]any)
		if !ok {
			t.Fatalf("channel search response=%#v", result)
		}
		return channels
	}
	publicResults := search(ownerName)
	if len(publicResults) != 1 {
		t.Fatalf("public search results=%#v", publicResults)
	}
	publicResult := publicResults[0].(map[string]any)
	for _, field := range []string{"id", "openim_group_id", "name", "description", "photo_url", "visibility", "member_count"} {
		if _, ok := publicResult[field]; !ok {
			t.Fatalf("rich channel search result missing %q: %#v", field, publicResult)
		}
	}
	if publicResult["visibility"] != "public" || publicResult["member_count"].(float64) < 1 {
		t.Fatalf("public channel search result=%#v", publicResult)
	}

	// Public channels allow direct join, but group-wide mute still blocks an
	// ordinary member from posting. The owner remains able to publish.
	openIMToken := issueDeviceOpenIMTokenForE2E(t, client, base, access)
	readerName := fmt.Sprintf("channel-reader-%d", time.Now().UnixNano())
	reader := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, readerName, password), http.StatusCreated)
	readerToken := issueDeviceOpenIMTokenForE2E(t, client, base, reader["access_token"].(string))
	readerID := openIMUserID(t, reader)
	postOpenIMJSON(t, client, base, "/group/join_group", readerToken, fmt.Sprintf(`{"groupID":%q,"reqMessage":"","joinSource":1,"inviterUserID":%q}`, publicGroupID, readerID))
	ordinaryPost := postOpenIMExpectError(t, client, base, "/msg/send_msg", readerToken, fmt.Sprintf(`{"sendID":%q,"groupID":%q,"sessionType":3,"contentType":101,"content":{"content":"member post must be blocked"}}`, readerID, publicGroupID))
	if ordinaryPost["errCode"] == float64(0) {
		t.Fatalf("ordinary channel member could post: %#v", ordinaryPost)
	}
	ownerID := openIMUserID(t, owner)
	postOpenIMJSON(t, client, base, "/msg/send_msg", openIMToken, fmt.Sprintf(`{"sendID":%q,"groupID":%q,"sessionType":3,"contentType":101,"content":{"content":"owner announcement"}}`, ownerID, publicGroupID))

	private := create("channel-private-"+ownerName, "private")
	privateChannel := private["channel"].(map[string]any)
	privateID := privateChannel["id"].(string)
	privateGroupID := privateChannel["openim_group_id"].(string)
	if !strings.HasPrefix(privateGroupID, "ch_") {
		t.Fatalf("private channel group ID=%q", privateGroupID)
	}
	if got := search("channel-private-" + ownerName); len(got) != 0 {
		t.Fatalf("private channel leaked in public search: %#v", got)
	}

	nonMember := reader
	privateDetailRequest, _ := http.NewRequest(http.MethodGet, base+"/v1/channels/"+privateID, nil)
	privateDetailRequest.Header.Set("Authorization", "Bearer "+nonMember["access_token"].(string))
	privateDetailResponse, err := client.Do(privateDetailRequest)
	if err != nil {
		t.Fatal(err)
	}
	privateDetailResponse.Body.Close()
	if privateDetailResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("private detail status=%d, want 404", privateDetailResponse.StatusCode)
	}
	privateReaderToken := issueDeviceOpenIMTokenForE2E(t, client, base, nonMember["access_token"].(string))
	postOpenIMJSON(t, client, base, "/group/join_group", privateReaderToken, fmt.Sprintf(`{"groupID":%q,"reqMessage":"private channel E2E","joinSource":1,"inviterUserID":%q}`, privateGroupID, readerID))
	privateDetailRequest, _ = http.NewRequest(http.MethodGet, base+"/v1/channels/"+privateID, nil)
	privateDetailRequest.Header.Set("Authorization", "Bearer "+nonMember["access_token"].(string))
	privateDetailResponse, err = client.Do(privateDetailRequest)
	if err != nil {
		t.Fatal(err)
	}
	privateDetailResponse.Body.Close()
	if privateDetailResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("private detail after join request status=%d, want 404", privateDetailResponse.StatusCode)
	}

	pinned := postAuthenticatedJSON(t, client, base+"/v1/channels/"+publicID+"/pins", access, `{"message_id":"channel-post-e2e-1"}`, http.StatusCreated)
	if pinned["pin"].(map[string]any)["message_id"] != "channel-post-e2e-1" {
		t.Fatalf("pin response=%#v", pinned)
	}
	pins := getChannelJSON(t, client, base+"/v1/channels/"+publicID+"/pins", access, http.StatusOK)
	if items, ok := pins["pins"].([]any); !ok || len(items) != 1 {
		t.Fatalf("pinned post list=%#v", pins)
	}
	request, _ := http.NewRequest(http.MethodDelete, base+"/v1/channels/"+publicID+"/pins/channel-post-e2e-1", nil)
	request.Header.Set("Authorization", "Bearer "+access)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unpin status=%d, want 204", response.StatusCode)
	}

	responseBody := postOpenIMExpectError(t, client, base, "/group/cancel_mute_group", openIMToken, fmt.Sprintf(`{"groupID":%q}`, publicGroupID))
	if responseBody["errCode"] == float64(0) {
		t.Fatalf("channel owner disabled read-only mode: %#v", responseBody)
	}
	// Private channels use the same `ch_` guard even when callers know the ID.
	responseBody = postOpenIMExpectError(t, client, base, "/group/cancel_mute_group", openIMToken, fmt.Sprintf(`{"groupID":%q}`, privateGroupID))
	if responseBody["errCode"] == float64(0) {
		t.Fatalf("private channel owner disabled read-only mode: %#v", responseBody)
	}
}

func postOpenIMExpectError(t *testing.T, client *http.Client, base, path, token, payload string) map[string]any {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, base+"/openim"+path, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", fmt.Sprintf("e2e-channel-%d", time.Now().UnixNano()))
	request.Header.Set("token", token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope map[string]any
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("OpenIM %s returned HTTP %d: %#v", path, response.StatusCode, envelope)
	}
	return envelope
}

func getChannelJSON(t *testing.T, client *http.Client, endpoint, token string, status int) map[string]any {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		t.Fatalf("GET %s status=%d body=%s, want %d", endpoint, response.StatusCode, body, status)
	}
	result := make(map[string]any)
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
