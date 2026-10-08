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
	"os"
	"regexp"
	"sync"
	"testing"
	"time"
)

func TestAuthProtocolAgainstDeployedAPI(t *testing.T) {
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Fatal("E2E_BASE_URL is required")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	get(t, client, baseURL+"/healthz", http.StatusOK)

	username := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	post(t, client, baseURL+"/v1/auth/verify-username", fmt.Sprintf(`{"username":%q}`, username), http.StatusOK)
	post(t, client, baseURL+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q}`, username, username+"@example.test"), http.StatusCreated)
	post(t, client, baseURL+"/v1/auth/verify-username", fmt.Sprintf(`{"username":%q}`, username), http.StatusOK)
	post(t, client, baseURL+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple"}`, username), http.StatusConflict)
	post(t, client, baseURL+"/v1/auth/signup", `{"username":"valid-name","password":"correct-horse-battery-staple","unknown":true}`, http.StatusUnprocessableEntity)
}

func TestMFAAndPasswordlessLoginAgainstDeployedAPI(t *testing.T) {
	base, mailpit := os.Getenv("E2E_BASE_URL"), os.Getenv("E2E_MAILPIT_URL")
	if base == "" || mailpit == "" {
		t.Fatal("E2E_BASE_URL and E2E_MAILPIT_URL are required")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	user := fmt.Sprintf("otp-%d", time.Now().UnixNano())
	email := user + "@example.test"
	post(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q}`, user, email), http.StatusCreated)
	challenge := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, email), http.StatusAccepted)["challenge_id"].(string)
	code := mailCode(t, client, mailpit, email)
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, challenge, code), http.StatusOK)
	login := postJSON(t, client, base+"/v1/auth/login/start", fmt.Sprintf(`{"email":%q}`, email), http.StatusAccepted)["challenge_id"].(string)
	code = mailCode(t, client, mailpit, email)
	tokens := postJSON(t, client, base+"/v1/auth/login/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q,"platform_id":"web","device_name":"e2e"}`, login, code), http.StatusOK)
	refresh := tokens["refresh_token"].(string)
	next := postJSON(t, client, base+"/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, refresh), http.StatusOK)
	if next["refresh_token"] == refresh || next["access_token"] == "" {
		t.Fatal("refresh rotation did not return replacement tokens")
	}
	image := uploadImage(t, client, base, tokens["access_token"].(string))
	request, _ := http.NewRequest(http.MethodGet, base+"/v1/profile/", nil)
	request.Header.Set("Authorization", "Bearer "+tokens["access_token"].(string))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("profile status=%d", response.StatusCode)
	}
	var profile map[string]any
	_ = json.NewDecoder(response.Body).Decode(&profile)
	if profile["image"] != image {
		t.Fatalf("profile image=%v want %q", profile["image"], image)
	}
}

func uploadImage(t *testing.T, c *http.Client, base, access string) string {
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
	_, _ = part.Write([]byte("not-a-real-png-but-valid-upload-contract"))
	_ = writer.Close()
	request, err := http.NewRequest(http.MethodPost, base+"/v1/profile/update", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+access)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("profile update status=%d", response.StatusCode)
	}
	var out map[string]string
	if err = json.NewDecoder(response.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out["image"]
}

func postJSON(t *testing.T, c *http.Client, url, body string, want int) map[string]any {
	t.Helper()
	r, err := c.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != want {
		t.Fatalf("POST %s: got %d", url, r.StatusCode)
	}
	var out map[string]any
	if err = json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func mailCode(t *testing.T, c *http.Client, base, email string) string {
	t.Helper()
	re := regexp.MustCompile(`\b\d{6}\b`)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := c.Get(base + "/api/v1/messages")
		if err == nil {
			var v struct {
				Messages []struct {
					To      []struct{ Address string }
					Snippet string
				}
			}
			_ = json.NewDecoder(r.Body).Decode(&v)
			r.Body.Close()
			for _, m := range v.Messages {
				for _, to := range m.To {
					if to.Address == email {
						if code := re.FindString(m.Snippet); code != "" {
							return code
						}
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no OTP for %s", email)
	return ""
}

// Exactly one concurrent request may reserve a username. This exercises the
// database uniqueness constraint and transaction boundary under a real service.
func TestConcurrentUsernameClaimsAgainstDeployedAPI(t *testing.T) {
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Fatal("E2E_BASE_URL is required")
	}
	username := fmt.Sprintf("race-%d", time.Now().UnixNano())
	const workers = 12
	results := make(chan int, workers)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			response, err := (&http.Client{Timeout: 10 * time.Second}).Post(baseURL+"/v1/auth/signup", "application/json", bytes.NewBufferString(fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple"}`, username)))
			if err != nil {
				results <- 0
				return
			}
			defer response.Body.Close()
			results <- response.StatusCode
		}()
	}
	close(start)
	group.Wait()
	close(results)
	created, conflicts, failures := 0, 0, 0
	for status := range results {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			failures++
		}
	}
	if created != 1 || conflicts != workers-1 || failures != 0 {
		t.Fatalf("created=%d conflicts=%d failures=%d", created, conflicts, failures)
	}
}

func TestSecurityAndDiscoveryProtocolsAgainstDeployedAPI(t *testing.T) {
	base, mailpit := os.Getenv("E2E_BASE_URL"), os.Getenv("E2E_MAILPIT_URL")
	if base == "" || mailpit == "" {
		t.Fatal("E2E_BASE_URL and E2E_MAILPIT_URL are required")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	user := fmt.Sprintf("security-%d", time.Now().UnixNano())
	email := user + "@example.test"
	post(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q}`, user, email), http.StatusCreated)
	signup := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, email), http.StatusAccepted)
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, signup["challenge_id"], mailCode(t, client, mailpit, email)), http.StatusOK)
	login := postJSON(t, client, base+"/v1/auth/login/start", fmt.Sprintf(`{"email":%q}`, email), http.StatusAccepted)
	tokens := postJSON(t, client, base+"/v1/auth/login/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q,"platform_id":"web"}`, login["challenge_id"], mailCode(t, client, mailpit, email)), http.StatusOK)
	access := tokens["access_token"].(string)

	getAuthenticated(t, client, base+"/v1/security/backup-codes", access, http.StatusOK)
	stepUp := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"step_up"}`, email), http.StatusAccepted)
	regenerated := postAuthenticatedJSON(t, client, base+"/v1/security/backup-codes/regenerate", access, fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, stepUp["challenge_id"], mailCode(t, client, mailpit, email)), http.StatusOK)
	if codes, ok := regenerated["recovery_codes"].([]any); !ok || len(codes) != 10 {
		t.Fatalf("recovery_codes=%v", regenerated["recovery_codes"])
	}
	getAuthenticated(t, client, base+"/v1/security/activity", access, http.StatusOK)
	peer := fmt.Sprintf("peer-%d", time.Now().UnixNano())
	peerEmail := peer + "@example.test"
	post(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q}`, peer, peerEmail), http.StatusCreated)
	peerSignup := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, peerEmail), http.StatusAccepted)
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, peerSignup["challenge_id"], mailCode(t, client, mailpit, peerEmail)), http.StatusOK)
	peerLogin := postJSON(t, client, base+"/v1/auth/login/start", fmt.Sprintf(`{"email":%q}`, peerEmail), http.StatusAccepted)
	peerTokens := postJSON(t, client, base+"/v1/auth/login/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q,"platform_id":"web"}`, peerLogin["challenge_id"], mailCode(t, client, mailpit, peerEmail)), http.StatusOK)
	discovered := postAuthenticatedJSON(t, client, base+"/v1/friends/discover-network", peerTokens["access_token"].(string), fmt.Sprintf(`{"emails":[%q]}`, email), http.StatusOK)
	if ids, ok := discovered["open_im_user_ids"].([]any); !ok || len(ids) != 1 {
		t.Fatalf("discovered OpenIM identities = %v", discovered["open_im_user_ids"])
	}
	postAuthenticatedJSON(t, client, base+"/v1/security/revoke", access, `{"all":true}`, http.StatusNoContent)
}

func getAuthenticated(t *testing.T, c *http.Client, url, access string, want int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
		t.Fatalf("GET %s: got %d want %d", url, resp.StatusCode, want)
	}
}

func postAuthenticatedJSON(t *testing.T, c *http.Client, url, access, body string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: got %d want %d: %s", url, resp.StatusCode, want, body)
	}
	if want == http.StatusNoContent {
		return nil
	}
	var out map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func get(t *testing.T, client *http.Client, url string, want int) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("GET %s: got %d, want %d", url, response.StatusCode, want)
	}
}
func post(t *testing.T, client *http.Client, url, body string, want int) {
	t.Helper()
	response, err := client.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("POST %s: got %d, want %d", url, response.StatusCode, want)
	}
}
