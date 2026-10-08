//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

const productionE2EQueueURL = "https://sqs.ap-south-1.amazonaws.com/403048695675/pingmessenger-auth-e2e"

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

// TestProductionCompleteAuthFlow uses the private SNS-to-SQS inbox to verify
// every OTP-dependent auth protocol against production. It must run alone:
// the queue is drained before each challenge because the SNS message body does
// not include the recipient address.
func TestProductionCompleteAuthFlow(t *testing.T) {
	base := productionBaseURL(t)
	inbox := newProductionInbox(t)
	client := &http.Client{Timeout: 15 * time.Second}
	name := fmt.Sprintf("prod-full-%d", time.Now().UnixNano())
	email := name + "@example.test"
	created := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q}`, name, email), http.StatusCreated)
	primaryOpenIMID := openIMUserID(t, created)

	inbox.drain(t)
	signup := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, email), http.StatusAccepted)
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, signup["challenge_id"], inbox.otp(t)), http.StatusOK)

	inbox.drain(t)
	login := postJSON(t, client, base+"/v1/auth/login/start", fmt.Sprintf(`{"email":%q}`, email), http.StatusAccepted)
	tokens := postJSON(t, client, base+"/v1/auth/login/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q,"platform_id":"web","device_name":"production-e2e"}`, login["challenge_id"], inbox.otp(t)), http.StatusOK)
	access, refresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	next := postJSON(t, client, base+"/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, refresh), http.StatusOK)
	if next["refresh_token"] == refresh || next["access_token"] == "" {
		t.Fatal("refresh token rotation failed")
	}
	primaryOpenIMToken := testProductionDeviceOpenIMBridge(t, client, base, access, primaryOpenIMID)

	getAuthenticated(t, client, base+"/v1/profile/", access, http.StatusOK)
	getAuthenticated(t, client, base+"/v1/security/backup-codes", access, http.StatusOK)
	getAuthenticated(t, client, base+"/v1/security/activity", access, http.StatusOK)
	inbox.drain(t)
	stepUp := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"step_up"}`, email), http.StatusAccepted)
	regenerated := postAuthenticatedJSON(t, client, base+"/v1/security/backup-codes/regenerate", access, fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, stepUp["challenge_id"], inbox.otp(t)), http.StatusOK)
	if codes, ok := regenerated["recovery_codes"].([]any); !ok || len(codes) != 10 {
		t.Fatalf("unexpected regenerated backup codes: %#v", regenerated)
	}
	image := uploadImage(t, client, base, access)
	profile := getJSONAuthenticated(t, client, base+"/v1/profile/", access)
	if profile["image"] != image {
		t.Fatalf("profile image = %#v; want %q", profile["image"], image)
	}
	// A separate verified user proves production contact discovery returns the
	// OpenIM identity only after both contacts are active.
	peerName := fmt.Sprintf("prod-peer-%d", time.Now().UnixNano())
	peerEmail := peerName + "@example.test"
	peerCreated := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":"correct-horse-battery-staple","email":%q}`, peerName, peerEmail), http.StatusCreated)
	peerOpenIMID := openIMUserID(t, peerCreated)
	inbox.drain(t)
	peerSignup := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, peerEmail), http.StatusAccepted)
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, peerSignup["challenge_id"], inbox.otp(t)), http.StatusOK)
	inbox.drain(t)
	peerLogin := postJSON(t, client, base+"/v1/auth/login/start", fmt.Sprintf(`{"email":%q}`, peerEmail), http.StatusAccepted)
	peerTokens := postJSON(t, client, base+"/v1/auth/login/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q,"platform_id":"web","device_name":"production-e2e-peer"}`, peerLogin["challenge_id"], inbox.otp(t)), http.StatusOK)
	discovered := postAuthenticatedJSON(t, client, base+"/v1/friends/discover-network", peerTokens["access_token"].(string), fmt.Sprintf(`{"emails":[%q]}`, email), http.StatusOK)
	if ids, ok := discovered["open_im_user_ids"].([]any); !ok || len(ids) != 1 {
		t.Fatalf("expected one discovered OpenIM identity: %#v", discovered)
	}
	peerOpenIMToken := testProductionDeviceOpenIMBridge(t, client, base, peerTokens["access_token"].(string), peerOpenIMID)
	testOpenIMFriendInteroperability(t, client, base, primaryOpenIMToken, peerOpenIMToken, primaryOpenIMID, peerOpenIMID)
	postAuthenticatedJSON(t, client, base+"/v1/security/revoke", access, `{"all":true}`, http.StatusNoContent)
}

func TestProductionEmailFactorGatesPasswordLogin(t *testing.T) {
	base := productionBaseURL(t)
	inbox := newProductionInbox(t)
	client := &http.Client{Timeout: 15 * time.Second}
	name := fmt.Sprintf("prod-mfa-email-%d", time.Now().UnixNano())
	email := name + "@example.test"
	password := "correct-horse-battery-staple"
	post(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q,"email":%q}`, name, password, email), http.StatusCreated)
	inbox.drain(t)
	signup := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, email), http.StatusAccepted)
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, signup["challenge_id"], inbox.otp(t)), http.StatusOK)
	baseTokens := postJSON(t, client, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, name, password), http.StatusOK)
	access := baseTokens["access_token"].(string)
	factor := postAuthenticatedJSON(t, client, base+"/v1/security/mfa/factors", access, fmt.Sprintf(`{"kind":"email","contact":%q}`, email), http.StatusCreated)
	if factor["status"] != "enabled" {
		t.Fatalf("email factor was not enabled: %#v", factor)
	}
	inbox.drain(t)
	pending := postJSON(t, client, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, name, password), http.StatusAccepted)
	if pending["mfa_required"] != true {
		t.Fatalf("password login did not require MFA: %#v", pending)
	}
	verified := postJSON(t, client, base+"/v1/auth/login/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, pending["challenge_id"], inbox.otp(t)), http.StatusOK)
	if verified["access_token"] == "" || verified["refresh_token"] == "" {
		t.Fatalf("email MFA did not issue tokens: %#v", verified)
	}
}

func testProductionDeviceOpenIMBridge(t *testing.T, client *http.Client, base, access, openIMUserID string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x, y := key.PublicKey.X.FillBytes(make([]byte, 32)), key.PublicKey.Y.FillBytes(make([]byte, 32))
	register := postAuthenticatedJSON(t, client, base+"/v1/openim/device-key", access, fmt.Sprintf(`{"public_key":{"kty":"EC","crv":"P-256","x":%q,"y":%q}}`, base64.RawURLEncoding.EncodeToString(x), base64.RawURLEncoding.EncodeToString(y)), http.StatusOK)
	fingerprint := register["fingerprint"].(string)
	issue := func() string {
		challenge := postAuthenticatedJSON(t, client, base+"/v1/openim/token/challenge", access, `{}`, http.StatusCreated)
		id, nonce, audience := challenge["challenge_id"].(string), challenge["nonce"].(string), challenge["audience"].(string)
		digest := sha256.Sum256([]byte("v1|" + id + "|" + nonce + "|" + sessionIDFromAccess(t, access) + "|" + audience))
		r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		sig := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
		payload := fmt.Sprintf(`{"challenge_id":%q,"nonce":%q,"signature":%q,"fingerprint":%q}`, id, nonce, base64.RawURLEncoding.EncodeToString(sig), fingerprint)
		issued := postAuthenticatedJSON(t, client, base+"/v1/openim/token/issue", access, payload, http.StatusOK)
		assertStatus(t, client, http.MethodPost, base+"/v1/openim/token/issue", payload, http.StatusUnauthorized, "device proof replay")
		return issued["token"].(string)
	}
	first := issue()
	probe := startOpenIMKickProbe(t, base, first, openIMUserID)
	second := issue()
	if result := probe.wait(t); result != "kick_message_binary" && result != "kick_closed" {
		t.Fatalf("reissued OpenIM token did not receive a kick signal: %s", result)
	}
	if first == second {
		t.Fatal("OpenIM reissue returned the prior token")
	}
	parseOpenIMToken(t, client, base, first, false)
	parseOpenIMToken(t, client, base, second, true)
	return second
}

type openIMKickProbe struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	result *bufio.Scanner
	stderr *bytes.Buffer
}

func startOpenIMKickProbe(t *testing.T, base, token, userID string) *openIMKickProbe {
	t.Helper()
	wsURL := strings.Replace(base, "https://", "wss://", 1) + "/openim/ws?" + url.Values{
		"token":        {token},
		"sendID":       {userID},
		"platformID":   {"5"},
		"operationID":  {fmt.Sprintf("e2e-ws-%d", time.Now().UnixNano())},
		"isBackground": {"false"},
		"isMsgResp":    {"true"},
	}.Encode()
	cmd := exec.Command("python3", "openim_ws_probe.py")
	stderr := new(bytes.Buffer)
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintln(stdin, wsURL); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		_ = cmd.Process.Kill()
		t.Fatalf("OpenIM WebSocket did not become ready: %q (%s)", scanner.Text(), strings.TrimSpace(stderr.String()))
	}
	return &openIMKickProbe{cmd: cmd, stdin: stdin, result: scanner, stderr: stderr}
}

func (p *openIMKickProbe) wait(t *testing.T) string {
	t.Helper()
	_ = p.stdin.Close()
	if !p.result.Scan() {
		err := p.cmd.Wait()
		t.Fatalf("OpenIM WebSocket ended without a kick result: %v (%s)", err, strings.TrimSpace(p.stderr.String()))
	}
	result := p.result.Text()
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("OpenIM WebSocket probe failed: %v (%s)", err, strings.TrimSpace(p.stderr.String()))
	}
	return result
}

func openIMUserID(t *testing.T, signup map[string]any) string {
	t.Helper()
	userID, ok := signup["user_id"].(string)
	if !ok || userID == "" {
		t.Fatalf("signup did not return user_id: %#v", signup)
	}
	return "pm" + strings.ReplaceAll(userID, "-", "")
}

// testOpenIMFriendInteroperability proves that a device-bound platform token
// works with the user-facing OpenIM SDK-compatible HTTP API. It also proves
// OpenIM enforces the token subject: each operation is sent by its owner.
func testOpenIMFriendInteroperability(t *testing.T, client *http.Client, base, primaryToken, peerToken, primaryID, peerID string) {
	t.Helper()
	postOpenIMJSON(t, client, base, "/friend/add_friend", primaryToken, fmt.Sprintf(`{"fromUserID":%q,"toUserID":%q,"reqMsg":"production device-bound E2E","ex":""}`, primaryID, peerID))
	postOpenIMJSON(t, client, base, "/friend/add_friend_response", peerToken, fmt.Sprintf(`{"fromUserID":%q,"toUserID":%q,"handleResult":1,"handleMsg":"accepted by production E2E"}`, primaryID, peerID))
	friends := postOpenIMJSON(t, client, base, "/friend/get_friend_list", primaryToken, fmt.Sprintf(`{"userID":%q,"pagination":{"pageNumber":1,"showNumber":100}}`, primaryID))
	data, _ := friends["data"].(map[string]any)
	list, _ := data["friendsInfo"].([]any)
	for _, item := range list {
		friend, _ := item.(map[string]any)
		friendUser, _ := friend["friendUser"].(map[string]any)
		if friendUser["userID"] == peerID {
			return
		}
	}
	t.Fatalf("accepted OpenIM friend %q absent from list: %#v", peerID, friends)
}

func postOpenIMJSON(t *testing.T, client *http.Client, base, path, token, payload string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/openim"+path, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("operationID", fmt.Sprintf("e2e-%d", time.Now().UnixNano()))
	req.Header.Set("token", token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var envelope map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode OpenIM %s response: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK || envelope["errCode"] != float64(0) {
		t.Fatalf("OpenIM %s failed (HTTP %d): %#v", path, resp.StatusCode, envelope)
	}
	return envelope
}

func sessionIDFromAccess(t *testing.T, access string) string {
	t.Helper()
	parts := strings.Split(access, ".")
	if len(parts) != 3 {
		t.Fatal("malformed access JWT")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		SessionID string `json:"sid"`
	}
	if err = json.Unmarshal(body, &claims); err != nil || claims.SessionID == "" {
		t.Fatal("access JWT lacks sid")
	}
	return claims.SessionID
}

func parseOpenIMToken(t *testing.T, client *http.Client, base, token string, valid bool) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/openim/auth/parse_token", bytes.NewBufferString(fmt.Sprintf(`{"token":%q}`, token)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("operationID", fmt.Sprintf("e2e-%d", time.Now().UnixNano()))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var envelope struct {
		ErrCode int `json:"errCode"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if valid && envelope.ErrCode != 0 {
		t.Fatalf("replacement OpenIM token was rejected: %#v", envelope)
	}
	if !valid && envelope.ErrCode == 0 {
		t.Fatal("revoked OpenIM token remained valid")
	}
}

type productionInbox struct{ queueURL string }

func newProductionInbox(t *testing.T) productionInbox {
	t.Helper()
	queue := os.Getenv("E2E_SNS_SQS_QUEUE_URL")
	if queue == "" {
		queue = productionE2EQueueURL
	}
	if queue != productionE2EQueueURL {
		t.Fatal("E2E_SNS_SQS_QUEUE_URL must be the dedicated PingMessenger E2E queue")
	}
	if _, err := exec.LookPath("aws"); err != nil {
		t.Fatalf("aws CLI is required for production OTP E2E: %v", err)
	}
	return productionInbox{queueURL: queue}
}

func (i productionInbox) drain(t *testing.T) {
	t.Helper()
	for n := 0; n < 10; n++ {
		messages := i.receive(t, 0)
		if len(messages) == 0 {
			return
		}
		for _, message := range messages {
			i.delete(t, message.ReceiptHandle)
		}
	}
	t.Fatal("dedicated SNS E2E queue did not drain")
}

func (i productionInbox) otp(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	code := regexp.MustCompile(`\b\d{6}\b`)
	for time.Now().Before(deadline) {
		for _, message := range i.receive(t, 10) {
			i.delete(t, message.ReceiptHandle)
			var envelope struct{ Message string }
			if json.Unmarshal([]byte(message.Body), &envelope) == nil {
				if match := code.FindString(envelope.Message); match != "" {
					return match
				}
			}
		}
	}
	t.Fatal("timed out waiting for SNS OTP in dedicated SQS queue")
	return ""
}

type sqsMessage struct {
	Body          string `json:"Body"`
	ReceiptHandle string `json:"ReceiptHandle"`
}

func (i productionInbox) receive(t *testing.T, wait int) []sqsMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "aws", "--profile", "chainsystems", "--region", "ap-south-1", "sqs", "receive-message", "--queue-url", i.queueURL, "--max-number-of-messages", "10", "--wait-time-seconds", fmt.Sprint(wait), "--visibility-timeout", "30", "--output", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("receive SNS E2E message: %v", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var response struct {
		Messages []sqsMessage `json:"Messages"`
	}
	if err = json.Unmarshal(out, &response); err != nil {
		t.Fatal(err)
	}
	return response.Messages
}
func (i productionInbox) delete(t *testing.T, receipt string) {
	t.Helper()
	if err := exec.Command("aws", "--profile", "chainsystems", "--region", "ap-south-1", "sqs", "delete-message", "--queue-url", i.queueURL, "--receipt-handle", receipt).Run(); err != nil {
		t.Fatalf("delete SNS E2E message: %v", err)
	}
}

func getJSONAuthenticated(t *testing.T, c *http.Client, endpoint, access string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: got %d", endpoint, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
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
