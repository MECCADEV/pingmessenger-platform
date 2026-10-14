//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type authenticatedJSONResponse struct {
	status int
	body   map[string]any
}

func getAuthenticatedJSONStatus(t *testing.T, client *http.Client, endpoint, access string) authenticatedJSONResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	return authenticatedJSONResponse{status: resp.StatusCode, body: out}
}

// TestChainObservationE2E is enabled only when a real finalized Solana
// transaction is supplied. It verifies the deployed API's asynchronous
// indexing path without baking a public transaction or account into tests.
func TestChainObservationE2E(t *testing.T) {
	chainID := strings.TrimSpace(os.Getenv("E2E_CHAIN_ID"))
	txID := strings.TrimSpace(os.Getenv("E2E_CHAIN_TRANSACTION_ID"))
	if chainID == "" || txID == "" {
		t.Skip("E2E_CHAIN_ID and E2E_CHAIN_TRANSACTION_ID are required")
	}
	base := strings.TrimRight(getE2EBaseURL(t), "/")
	client := &http.Client{Timeout: 15 * time.Second}
	password := "correct-horse-battery-staple"
	newUser := func(prefix string) map[string]any {
		name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
		return postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password), http.StatusCreated)
	}
	sender := newUser("chain-index-sender")
	receiver := newUser("chain-index-receiver")
	paymentID := fmt.Sprintf("e2e-chain-%d", time.Now().UnixNano())
	conversationID := fmt.Sprintf("e2e-chain-conversation-%d", time.Now().UnixNano())
	postAuthenticatedJSON(t, client, base+"/v1/conversations/"+conversationID+"/payments", sender["access_token"].(string), fmt.Sprintf(`{"payment_id":%q,"to_user_id":%q,"payment_type":"direct","chain_id":%q,"transaction_id":%q,"asset":"SOL","amount_atomic":"1"}`, paymentID, receiver["user_id"], chainID, txID), http.StatusCreated)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response := getAuthenticatedJSONStatus(t, client, base+"/v1/payments/"+paymentID+"/chain", sender["access_token"].(string))
		if response.status == http.StatusOK {
			if _, ok := response.body["transfers"].([]any); !ok {
				t.Fatalf("chain projection missing transfers: %#v", response.body)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("chain transaction was not indexed before timeout")
}
