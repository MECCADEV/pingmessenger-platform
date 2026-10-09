//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
)

func TestUsernameAvailabilityWebSocketAgainstDeployedAPI(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		t.Skip("E2E_BASE_URL is not configured")
	}
	wsURL := "ws" + base[len("http"):] + "/v1/auth/username/availability"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	availableName := fmt.Sprintf("ws-available-%d", time.Now().UnixNano())
	if err = conn.WriteJSON(map[string]string{"type": "check_username", "request_id": "one", "username": availableName}); err != nil {
		t.Fatal(err)
	}
	var available map[string]any
	if err = conn.ReadJSON(&available); err != nil {
		t.Fatal(err)
	}
	if available["type"] != "username_availability" || available["available"] != true || available["request_id"] != "one" {
		t.Fatalf("unexpected availability response: %#v", available)
	}
	created := postJSON(t, &http.Client{Timeout: 15 * time.Second}, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","platform_id":"web"}`, availableName), http.StatusCreated)
	if created["ping_id"] == "" {
		t.Fatalf("signup did not return ping_id: %#v", created)
	}
	updatedName := fmt.Sprintf("ws-updated-%d", time.Now().UnixNano())
	updated := patchAuthenticatedJSON(t, &http.Client{Timeout: 15 * time.Second}, base+"/v1/profile/username", created["access_token"].(string), fmt.Sprintf(`{"username":%q}`, updatedName), http.StatusOK)
	if updated["username"] != updatedName {
		t.Fatalf("username update failed: %#v", updated)
	}
	if err = conn.WriteJSON(map[string]string{"type": "check_username", "request_id": "two", "username": updatedName}); err != nil {
		t.Fatal(err)
	}
	var taken map[string]any
	if err = conn.ReadJSON(&taken); err != nil {
		t.Fatal(err)
	}
	if taken["type"] != "username_availability" || taken["available"] != false || taken["request_id"] != "two" {
		t.Fatalf("unexpected taken response: %#v", taken)
	}
	if err = conn.WriteJSON(map[string]string{"type": "check_username", "request_id": "bad", "username": "ab"}); err != nil {
		t.Fatal(err)
	}
	var invalid map[string]any
	if err = conn.ReadJSON(&invalid); err != nil {
		t.Fatal(err)
	}
	if invalid["type"] != "error" || invalid["code"] != "invalid_username" {
		t.Fatalf("unexpected invalid response: %#v", invalid)
	}
}
