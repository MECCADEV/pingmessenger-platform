//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestProductionAuthContract exercises every public auth boundary that can be
// verified without reading a production OTP. It is deliberately opt-in so a
// developer cannot create real accounts or publish SNS messages by accident.
func TestProductionAuthContract(t *testing.T) {
	base := productionBaseURL(t)
	client := &http.Client{Timeout: 15 * time.Second}

	get(t, client, base+"/healthz", http.StatusOK)
	name := fmt.Sprintf("prod-e2e-%d", time.Now().UnixNano())
	email := name + "@example.test"

	assertStatus(t, client, http.MethodPost, base+"/v1/auth/signup", `{"username":"ab","password":"correct-horse-battery-staple"}`, http.StatusUnprocessableEntity, "short username")
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"short"}`, name), http.StatusUnprocessableEntity, "short password")
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","unknown":true}`, name), http.StatusUnprocessableEntity, "unknown signup field")
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q,"phone":"+15551234567"}`, name, email), http.StatusUnprocessableEntity, "multiple signup contacts")

	available := postJSON(t, client, base+"/v1/auth/verify-username", fmt.Sprintf(`{"username":%q}`, name), http.StatusOK)
	if available["available"] != true {
		t.Fatalf("username %q was unexpectedly unavailable: %#v", name, available)
	}
	created := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q}`, name, email), http.StatusCreated)
	if created["openim_sync"] != "complete" || created["status"] != "pending_contact_verification" {
		t.Fatalf("signup did not complete OpenIM provisioning: %#v", created)
	}
	post(t, client, base+"/v1/auth/verify-username", fmt.Sprintf(`{"username":%q}`, name), http.StatusOK)
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple"}`, name), http.StatusConflict, "duplicate username")

	// A successful challenge proves the live SNS Publish call succeeded. The
	// code remains unreadable to this harness by design.
	challenge := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, email), http.StatusAccepted)
	if _, ok := challenge["challenge_id"].(string); !ok {
		t.Fatalf("SNS challenge did not return an ID: %#v", challenge)
	}
	assertStatus(t, client, http.MethodPost, base+"/v1/mfa/challenge", `{"email":"invalid","purpose":"signup_contact_verification"}`, http.StatusUnprocessableEntity, "invalid challenge email")
	assertStatus(t, client, http.MethodPost, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"unsupported"}`, email), http.StatusUnprocessableEntity, "invalid challenge purpose")
	assertStatus(t, client, http.MethodPost, base+"/v1/mfa/verify", `{"challenge_id":"not-a-uuid","code":"abc"}`, http.StatusUnprocessableEntity, "invalid OTP format")

	// Login start intentionally remains indistinguishable for unknown contacts.
	unknown := postJSON(t, client, base+"/v1/auth/login/start", fmt.Sprintf(`{"email":"unknown-%d@example.test"}`, time.Now().UnixNano()), http.StatusAccepted)
	if unknown["status"] != "if eligible, a verification code was sent" {
		t.Fatalf("login start leaks account state: %#v", unknown)
	}
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/login/verify", `{"challenge_id":"not-a-uuid","code":"abc","platform_id":"production-e2e"}`, http.StatusUnprocessableEntity, "invalid login OTP format")
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/refresh", `{"refresh_token":"short"}`, http.StatusUnprocessableEntity, "invalid refresh format")
	assertStatus(t, client, http.MethodPost, base+"/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, strings.Repeat("x", 48)), http.StatusUnauthorized, "unknown refresh token")

	for _, path := range []string{"/v1/profile/", "/v1/security/backup-codes", "/v1/security/activity"} {
		assertStatus(t, client, http.MethodGet, base+path, "", http.StatusUnauthorized, "unauthenticated "+path)
	}
	assertStatus(t, client, http.MethodPost, base+"/v1/profile/update", `{}`, http.StatusUnauthorized, "unauthenticated profile update")
	assertStatus(t, client, http.MethodPost, base+"/v1/security/revoke", `{"all":true}`, http.StatusUnauthorized, "unauthenticated session revoke")
}

func productionBaseURL(t *testing.T) string {
	t.Helper()
	if os.Getenv("E2E_PRODUCTION") != "1" {
		t.Skip("set E2E_PRODUCTION=1 to run live production checks")
	}
	base := os.Getenv("E2E_BASE_URL")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host != "api-platform-pingmessenger.meainternal.com" || u.Path != "" {
		t.Fatal("E2E_BASE_URL must be exactly https://api-platform-pingmessenger.meainternal.com")
	}
	return strings.TrimRight(base, "/")
}

func assertStatus(t *testing.T, client *http.Client, method, endpoint, body string, want int, label string) {
	t.Helper()
	req, err := http.NewRequest(method, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var payload any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		t.Fatalf("%s: got %d want %d: %#v", label, resp.StatusCode, want, payload)
	}
}
