//go:build e2e

package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"google.golang.org/protobuf/encoding/protowire"
)

// TestOpenIMModifyAndRevokeReceiverEvents proves the fork's actual
// msg/modify_message and msg/revoke_msg paths update the receiver socket.
func TestOpenIMModifyAndRevokeReceiverEvents(t *testing.T) {
	base := strings.TrimRight(getE2EBaseURL(t), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	password := "correct-horse-battery-staple"
	newUser := func(prefix string) map[string]any {
		name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
		return postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password), http.StatusCreated)
	}
	sender := newUser("message-edit-sender")
	receiver := newUser("message-edit-receiver")
	senderID, receiverID := openIMUserID(t, sender), openIMUserID(t, receiver)
	senderToken := issueDeviceOpenIMTokenForE2E(t, client, base, sender["access_token"].(string))
	receiverToken := issueDeviceOpenIMTokenForE2E(t, client, base, receiver["access_token"].(string))
	testOpenIMFriendInteroperability(t, client, base, senderToken, receiverToken, senderID, receiverID)
	senderSocket := dialOpenIMClient(t, base, senderToken, senderID)
	defer senderSocket.Close()
	receiverSocket := dialOpenIMClient(t, base, receiverToken, receiverID)
	defer receiverSocket.Close()

	conversationID := singleConversationID(senderID, receiverID)
	original := `{"text":"original"}`
	requestData := encodeE2EMessageData(senderID, receiverID, original)
	request, err := jsonMessageFrame(senderToken, senderID, requestData)
	if err != nil {
		t.Fatal(err)
	}
	if err = senderSocket.WriteMessage(websocket.BinaryMessage, request); err != nil {
		t.Fatal(err)
	}
	if reply := readOpenIMJSONFrame(t, senderSocket, 20*time.Second); reply["errCode"] != float64(0) {
		t.Fatalf("send message failed: %#v", reply)
	}
	seq := readOpenIMMessageSeq(t, receiverSocket, original, 20*time.Second)
	if seq == 0 {
		t.Fatal("receiver message did not contain a sequence")
	}

	postOpenIMJSON(t, client, base, "/msg/modify_message", senderToken, fmt.Sprintf(`{"conversationID":%q,"seq":%d,"userID":%q,"oldContent":%q,"newContent":%q}`, conversationID, seq, senderID, original, `{"text":"edited"}`))
	modified := readOpenIMNotification(t, receiverSocket, 2103, 20*time.Second)
	if !bytes.Contains(modified, []byte(`{"text":"edited"}`)) {
		t.Fatalf("modify notification did not contain new content: %q", modified)
	}

	postOpenIMJSON(t, client, base, "/msg/revoke_msg", senderToken, fmt.Sprintf(`{"conversationID":%q,"seq":%d,"userID":%q}`, conversationID, seq, senderID))
	_ = readOpenIMNotification(t, receiverSocket, 2101, 20*time.Second)
}

func singleConversationID(a, b string) string {
	ids := []string{a, b}
	sort.Strings(ids)
	return "si_" + strings.Join(ids, "_")
}

func jsonMessageFrame(token, senderID string, data []byte) ([]byte, error) {
	return marshalJSON(map[string]any{
		"reqIdentifier": 1003,
		"token":         token,
		"sendID":        senderID,
		"operationID":   fmt.Sprintf("e2e-edit-%d", time.Now().UnixNano()),
		"msgIncr":       senderID + "_edit",
		"data":          data,
	})
}

func marshalJSON(value any) ([]byte, error) {
	// Keep the E2E frame helper independent of the platform's JSON package.
	return json.Marshal(value)
}

func readOpenIMMessageSeq(t *testing.T, conn *websocket.Conn, content string, timeout time.Duration) int64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		frame := readOpenIMJSONFrame(t, conn, time.Until(deadline))
		if frame["reqIdentifier"] != float64(2001) {
			continue
		}
		encoded, _ := frame["data"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(decoded, []byte(content)) {
			continue
		}
		return int64(openIMFieldVarint(decoded, 14))
	}
	t.Fatal("timed out waiting for receiver message")
	return 0
}

func readOpenIMNotification(t *testing.T, conn *websocket.Conn, contentType uint64, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		frame := readOpenIMJSONFrame(t, conn, time.Until(deadline))
		if frame["reqIdentifier"] != float64(2001) {
			continue
		}
		encoded, _ := frame["data"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if openIMFieldVarint(decoded, 11) == contentType {
			return decoded
		}
	}
	t.Fatalf("timed out waiting for OpenIM contentType=%d notification", contentType)
	return nil
}

func openIMFieldVarint(data []byte, wanted protowire.Number) uint64 {
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return 0
		}
		data = data[n:]
		if number == wanted && kind == protowire.VarintType {
			value, _ := protowire.ConsumeVarint(data)
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
