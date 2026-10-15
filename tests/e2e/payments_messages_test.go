//go:build e2e

package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"google.golang.org/protobuf/encoding/protowire"
)

// TestPaymentCustomMessageReachesOpenIMReceiver proves that the platform's
// durable payment projection is also delivered as the OpenIM custom-message
// wire type (contentType=110), not only through the platform payment stream.
func TestPaymentCustomMessageReachesOpenIMReceiver(t *testing.T) {
	base := strings.TrimRight(getE2EBaseURL(t), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	password := "correct-horse-battery-staple"
	newUser := func(prefix string) map[string]any {
		name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
		return postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password), http.StatusCreated)
	}
	sender := newUser("payment-openim-sender")
	receiver := newUser("payment-openim-receiver")
	senderOpenIMID, receiverOpenIMID := openIMUserID(t, sender), openIMUserID(t, receiver)
	senderOpenIMToken := issueDeviceOpenIMTokenForE2E(t, client, base, sender["access_token"].(string))
	receiverOpenIMToken := issueDeviceOpenIMTokenForE2E(t, client, base, receiver["access_token"].(string))
	testOpenIMFriendInteroperability(t, client, base, senderOpenIMToken, receiverOpenIMToken, senderOpenIMID, receiverOpenIMID)

	receiverSocket := dialOpenIMClient(t, base, receiverOpenIMToken, receiverOpenIMID)
	defer receiverSocket.Close()
	conversationID := fmt.Sprintf("e2e-openim-payment-%d", time.Now().UnixNano())
	paymentID := fmt.Sprintf("e2e-openim-payment-id-%d", time.Now().UnixNano())
	postAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/payments", sender["access_token"].(string), fmt.Sprintf(`{"payment_id":%q,"to_user_id":%q,"payment_type":"direct","chain_id":"e2e-devnet","transaction_id":%q,"asset":"TEST","amount_atomic":"100"}`, paymentID, receiver["user_id"].(string), paymentID+"-tx"), http.StatusCreated)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		frame := readOpenIMJSONFrame(t, receiverSocket, time.Until(deadline))
		if frame["reqIdentifier"] != float64(2001) {
			continue
		}
		encoded, ok := frame["data"].(string)
		if !ok {
			t.Fatalf("OpenIM payment push has no data: %#v", frame)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if got := openIMContentType(decoded); got != 110 {
			t.Fatalf("payment push content type=%d want 110: %#v", got, frame)
		}
		if !bytes.Contains(decoded, []byte("pingmessenger.payment.v1")) || !bytes.Contains(decoded, []byte(paymentID)) {
			t.Fatalf("payment push did not contain the payment schema/id: %q", decoded)
		}
		return
	}
	t.Fatal("timed out waiting for payment custom message on recipient OpenIM WebSocket")
}

func openIMContentType(data []byte) uint64 {
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return 0
		}
		data = data[n:]
		if number == 11 && kind == protowire.VarintType {
			value, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return 0
			}
			return value
		}
		n = protowire.ConsumeFieldValue(number, kind, data)
		if n < 0 {
			return 0
		}
		data = data[n:]
	}
	return 0
}

// TestPaymentAndDisappearingMessageContracts exercises the durable payment
// projection/recovery endpoints and the live disappearing-message notification
// stream. It is opt-in because it creates two accounts and ledger rows.
func TestPaymentAndDisappearingMessageContracts(t *testing.T) {
	base := strings.TrimRight(getE2EBaseURL(t), "/")
	client := &http.Client{Timeout: 15 * time.Second}
	password := "correct-horse-battery-staple"
	newUser := func(prefix string) map[string]any {
		name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
		return postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password), http.StatusCreated)
	}
	sender := newUser("payment-sender")
	receiver := newUser("payment-receiver")
	senderToken := sender["access_token"].(string)
	receiverToken := receiver["access_token"].(string)
	receiverID := receiver["user_id"].(string)
	conversationID := fmt.Sprintf("e2e-payment-%d", time.Now().UnixNano())

	// The stream is opened before the update so the test verifies notification
	// delivery, not just persistence.
	wsURL := strings.Replace(base, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1) + "/v1/conversations/" + conversationID + "/disappearing/ws"
	header := http.Header{"Authorization": []string{"Bearer " + senderToken}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	setting := putAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/disappearing", senderToken, `{"duration_seconds":3600,"enabled":true}`, http.StatusOK)
	if setting["enabled"] != true || setting["duration_seconds"] != float64(3600) {
		t.Fatalf("unexpected disappearing setting: %#v", setting)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var event map[string]any
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatalf("read disappearing notification: %v", err)
	}
	if event["type"] != "disappearing.updated" {
		t.Fatalf("unexpected disappearing event: %#v", event)
	}

	paymentID := fmt.Sprintf("e2e-payment-%d", time.Now().UnixNano())
	transactionID := strings.TrimSpace(os.Getenv("E2E_TRANSACTION_ID"))
	if transactionID == "" {
		transactionID = paymentID
	}
	created := postAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/payments", senderToken, fmt.Sprintf(`{"payment_id":%q,"to_user_id":%q,"payment_type":"direct","chain_id":"e2e-devnet","transaction_id":%q,"asset":"TEST","amount_atomic":"100"}`, paymentID, receiverID, transactionID), http.StatusCreated)
	payment, ok := created["payment"].(map[string]any)
	if !ok || payment["payment_id"] != paymentID || payment["status"] != "pending" {
		t.Fatalf("unexpected payment creation response: %#v", created)
	}
	if status, ok := created["message_status"].(string); !ok || (status != "sent" && status != "pending") {
		t.Fatalf("unexpected payment message status: %#v", created["message_status"])
	}
	paymentWSURL := strings.Replace(base, "https://", "wss://", 1)
	paymentWSURL = strings.Replace(paymentWSURL, "http://", "ws://", 1) + "/v1/conversations/" + conversationID + "/payments/ws"
	paymentConn, _, err := websocket.DefaultDialer.Dial(paymentWSURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer paymentConn.Close()
	receiverHeader := http.Header{"Authorization": []string{"Bearer " + receiverToken}}
	receiverPaymentConn, _, err := websocket.DefaultDialer.Dial(paymentWSURL, receiverHeader)
	if err != nil {
		t.Fatal(err)
	}
	defer receiverPaymentConn.Close()
	paymentConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var paymentEvent map[string]any
	if err := paymentConn.ReadJSON(&paymentEvent); err != nil {
		t.Fatalf("read payment WebSocket snapshot: %v", err)
	}
	if paymentEvent["type"] != "payment.updated" {
		t.Fatalf("unexpected payment WebSocket event: %#v", paymentEvent)
	}
	if got := paymentEvent["payment"].(map[string]any)["payment_id"]; got != paymentID {
		t.Fatalf("sender snapshot payment id = %v, want %s", got, paymentID)
	}
	if event := readPaymentEvent(t, receiverPaymentConn, paymentID, "pending", 5*time.Second); event == nil {
		t.Fatal("recipient did not receive the pending payment event")
	}
	if secret := strings.TrimSpace(os.Getenv("PINGPAY_EVENT_SECRET")); secret != "" {
		occurred := time.Now().UTC().Format(time.RFC3339Nano)
		body := fmt.Sprintf(`{"event_id":%q,"payment_id":%q,"status":"submitted","version":2,"occurred_at":%q}`, fmt.Sprintf("e2e-event-%d", time.Now().UnixNano()), paymentID, occurred)
		postSignedPaymentEvent(t, client, base, secret, body)
		if event := readPaymentEvent(t, paymentConn, paymentID, "submitted", 5*time.Second); event == nil {
			t.Fatal("sender did not receive submitted payment event")
		}
		if event := readPaymentEvent(t, receiverPaymentConn, paymentID, "submitted", 5*time.Second); event == nil {
			t.Fatal("recipient did not receive submitted payment event")
		}

		occurred = time.Now().UTC().Format(time.RFC3339Nano)
		body = fmt.Sprintf(`{"event_id":%q,"payment_id":%q,"status":"confirmed","version":3,"occurred_at":%q,"block_id":"e2e-block-3"}`, fmt.Sprintf("e2e-event-%d", time.Now().UnixNano()), paymentID, occurred)
		postSignedPaymentEvent(t, client, base, secret, body)
		if event := readPaymentEvent(t, paymentConn, paymentID, "confirmed", 5*time.Second); event == nil {
			t.Fatal("sender did not receive confirmed payment event")
		}
		if event := readPaymentEvent(t, receiverPaymentConn, paymentID, "confirmed", 5*time.Second); event == nil {
			t.Fatal("recipient did not receive confirmed payment event")
		}
	}

	// A short-lived record verifies the one-second timeout sweep. Both clients
	// must see the terminal failed event and its explicit timeout reason.
	timeoutPaymentID := fmt.Sprintf("e2e-timeout-%d", time.Now().UnixNano())
	timeoutTxID := timeoutPaymentID + "-tx"
	postAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/payments", senderToken, fmt.Sprintf(`{"payment_id":%q,"to_user_id":%q,"payment_type":"direct","chain_id":"e2e-timeout","transaction_id":%q,"amount_atomic":"101","expires_in_seconds":30}`, timeoutPaymentID, receiverID, timeoutTxID), http.StatusCreated)
	if event := readPaymentEvent(t, paymentConn, timeoutPaymentID, "failed", 40*time.Second); event == nil {
		t.Fatal("sender did not receive timeout failure")
	} else if event["payment"].(map[string]any)["update_reason"] != "timeout" {
		t.Fatalf("sender timeout event missing reason: %#v", event)
	}
	if event := readPaymentEvent(t, receiverPaymentConn, timeoutPaymentID, "failed", 5*time.Second); event == nil {
		t.Fatal("recipient did not receive timeout failure")
	}

	synced := getAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/payments/sync?to=latest&limit=10", senderToken, http.StatusOK)
	items, ok := synced["payments"].([]any)
	if !ok || len(items) < 2 {
		t.Fatalf("payment sync did not return the created payment: %#v", synced)
	}
	page := getAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/payments?to=latest&limit=1", senderToken, http.StatusOK)
	pageItems := page["payments"].([]any)
	if len(pageItems) != 1 {
		t.Fatalf("expected one payment in first page: %#v", page)
	}
	next := page["next_to"].(string)
	if next == "" {
		t.Fatal("first page did not expose next_to")
	}
	secondPage := getAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/payments?to=latest&last_known_payment_id="+next+"&limit=10", senderToken, http.StatusOK)
	if len(secondPage["payments"].([]any)) == 0 {
		t.Fatalf("exclusive last_known_payment_id cursor returned no older page: %#v", secondPage)
	}
	get := getAuthenticatedJSON(t, client, base+"/v1/payments/"+paymentID, senderToken, http.StatusOK)
	if got := get["payment"].(map[string]any)["payment_id"]; got != paymentID {
		t.Fatalf("payment lookup returned %v", got)
	}

	timers := getAuthenticatedJSON(t, client, base+"/v1/settings/timers", senderToken, http.StatusOK)
	if _, ok := timers["timers"].([]any); !ok {
		t.Fatalf("timer response missing timers: %#v", timers)
	}
}

func postSignedPaymentEvent(t *testing.T, client *http.Client, base, secret, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/internal/payments/events", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	req.Header.Set("X-PingPay-Signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("payment update status=%d", resp.StatusCode)
	}
}

func readPaymentEvent(t *testing.T, conn *websocket.Conn, paymentID, status string, timeout time.Duration) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			return nil
		}
		if event["type"] != "payment.updated" {
			continue
		}
		payment, ok := event["payment"].(map[string]any)
		if !ok || payment["payment_id"] != paymentID || payment["status"] != status {
			continue
		}
		return event
	}
}

func getE2EBaseURL(t *testing.T) string {
	t.Helper()
	if base := strings.TrimSpace(os.Getenv("E2E_BASE_URL")); base != "" {
		return base
	}
	t.Skip("E2E_BASE_URL is not configured")
	return ""
}

func putAuthenticatedJSON(t *testing.T, c *http.Client, endpoint, access, body string, want int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("PUT %s: got %d want %d", endpoint, resp.StatusCode, want)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
