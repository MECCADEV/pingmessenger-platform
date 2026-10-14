//go:build e2e

package e2e

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
)

// TestProductionOpenIMMultipartDocumentUpload exercises the exact object flow
// used by the OpenIM Android/Flutter SDK. It is intentionally a real upload,
// rather than a status-code route probe: the test obtains a device-bound token,
// creates a multipart upload, signs both parts, uploads both parts to S3,
// completes the upload, obtains an access URL, and verifies the downloaded
// bytes. A file larger than the server's minimum part size is used so that the
// auth_sign endpoint is exercised as well.
func TestProductionOpenIMMultipartDocumentUpload(t *testing.T) {
	base := productionBaseURL(t)
	client := &http.Client{Timeout: 45 * time.Second}
	password := "correct-horse-battery-staple"
	name := fmt.Sprintf("prod-doc-%d", time.Now().UnixNano())
	created := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password), http.StatusCreated)
	access, ok := created["access_token"].(string)
	if !ok || access == "" {
		t.Fatalf("signup did not return an access token: %#v", created)
	}
	userID := openIMUserID(t, created)
	token := issueDeviceOpenIMTokenForE2E(t, client, base, access)
	receiverName := fmt.Sprintf("prod-doc-receiver-%d", time.Now().UnixNano())
	receiverCreated := postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q}`, receiverName, password), http.StatusCreated)
	receiverID := openIMUserID(t, receiverCreated)
	receiverToken := issueDeviceOpenIMTokenForE2E(t, client, base, receiverCreated["access_token"].(string))

	limit := postOpenIMJSON(t, client, base, "/object/part_limit", token, `{}`)
	limitData := openIMDataMap(t, limit, "part_limit")
	minPartSize := int64(openIMNumber(t, limitData, "minPartSize"))
	if minPartSize <= 0 {
		t.Fatalf("OpenIM returned an invalid minimum part size: %#v", limitData)
	}

	// Two parts prove the complete multipart path without creating a large test
	// object. The second part is deliberately non-empty so S3 accepts it.
	file := bytes.Repeat([]byte("pingmessenger-document-e2e\n"), int((minPartSize+1024)/27))
	if int64(len(file)) <= minPartSize {
		file = append(file, bytes.Repeat([]byte("x"), int(minPartSize-int64(len(file))+1))...)
	}
	// Avoid OpenIM's content-addressed deduplication when the E2E test is run
	// repeatedly against the same production bucket.
	file = append(file, []byte(fmt.Sprintf("nonce-%d\n", time.Now().UnixNano()))...)
	partSize := minPartSize
	partHashes := make([]string, 0, 2)
	partSizes := []int{int(partSize), len(file) - int(partSize)}
	for offset, size := 0, 0; offset < len(file); offset += size {
		size = len(file) - offset
		if size > int(partSize) {
			size = int(partSize)
		}
		sum := md5.Sum(file[offset : offset+size])
		partHashes = append(partHashes, hex.EncodeToString(sum[:]))
	}
	overall := md5.Sum([]byte(strings.Join(partHashes, ",")))
	overallHash := hex.EncodeToString(overall[:])
	documentName := fmt.Sprintf("%s/document-%d.pdf", userID, time.Now().UnixNano())

	initiated := postOpenIMJSON(t, client, base, "/object/initiate_multipart_upload", token, fmt.Sprintf(`{"hash":%q,"size":%d,"partSize":%d,"maxParts":2,"name":%q,"contentType":"application/pdf"}`, overallHash, len(file), partSize, documentName))
	initiatedData := openIMDataMap(t, initiated, "initiate_multipart_upload")
	upload, ok := initiatedData["upload"].(map[string]any)
	if !ok {
		t.Fatalf("multipart initiate did not return upload state: %#v", initiatedData)
	}
	uploadID, ok := upload["uploadID"].(string)
	if !ok || uploadID == "" {
		t.Fatalf("multipart initiate returned no upload ID: %#v", upload)
	}

	signed := postOpenIMJSON(t, client, base, "/object/auth_sign", token, fmt.Sprintf(`{"uploadID":%q,"partNumbers":[1,2]}`, uploadID))
	signedData := openIMDataMap(t, signed, "auth_sign")
	parts, ok := signedData["parts"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("auth_sign did not return two signed parts: %#v", signedData)
	}

	etags := make([]string, 0, len(parts))
	offset := 0
	for i, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			t.Fatalf("auth_sign part %d has invalid shape: %#v", i+1, rawPart)
		}
		partURL := openIMString(part, "url")
		if partURL == "" {
			partURL = openIMString(signedData, "url")
		}
		partURL = signedObjectURL(t, partURL, signedData, part)
		body := file[offset : offset+partSizes[i]]
		req, err := http.NewRequest(http.MethodPut, partURL, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		applyOpenIMKeyValues(req, signedData["header"])
		applyOpenIMKeyValues(req, part["header"])
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("upload part %d: %v", i+1, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("upload part %d returned HTTP %d", i+1, resp.StatusCode)
		}
		etag := strings.Trim(resp.Header.Get("ETag"), `"`)
		if etag == "" {
			t.Fatalf("upload part %d returned no ETag", i+1)
		}
		etags = append(etags, etag)
		offset += partSizes[i]
	}

	completed := postOpenIMJSON(t, client, base, "/object/complete_multipart_upload", token, fmt.Sprintf(`{"uploadID":%q,"parts":%s,"name":%q,"contentType":"application/pdf"}`, uploadID, mustJSON(t, etags), documentName))
	completedData := openIMDataMap(t, completed, "complete_multipart_upload")
	if openIMString(completedData, "url") == "" {
		t.Fatalf("multipart completion returned no object URL: %#v", completedData)
	}

	accessURL := postOpenIMJSON(t, client, base, "/object/access_url", token, fmt.Sprintf(`{"name":%q}`, documentName))
	accessData := openIMDataMap(t, accessURL, "access_url")
	downloader := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return nil }}
	resp, err := downloader.Get(openIMString(accessData, "url"))
	if err != nil {
		t.Fatalf("download uploaded document: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, file) {
		t.Fatalf("downloaded document mismatch: HTTP %d, got %d bytes, want %d", resp.StatusCode, len(got), len(file))
	}

	// Complete the client-facing path: the uploaded object URL is embedded in
	// the contentType=105 file message and delivered over OpenIM WebSocket.
	receiver := dialOpenIMClient(t, base, receiverToken, receiverID)
	defer receiver.Close()
	sender := dialOpenIMClient(t, base, token, userID)
	defer sender.Close()
	time.Sleep(2 * time.Second)
	clientMsg := fmt.Sprintf("document-e2e-%d", time.Now().UnixNano())
	fileElem := fmt.Sprintf(`{"sourceUrl":%q,"fileName":"document-e2e.pdf","fileSize":%d,"uuid":%q}`, openIMString(accessData, "url"), len(file), clientMsg)
	msgData := encodeE2EMessageDataWithContentType(userID, receiverID, fileElem, 105)
	request, err := json.Marshal(map[string]any{
		"reqIdentifier": 1003,
		"token":         token,
		"sendID":        userID,
		"operationID":   fmt.Sprintf("prod-doc-msg-%d", time.Now().UnixNano()),
		"msgIncr":       userID + "_" + clientMsg,
		"data":          msgData,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = sender.WriteMessage(websocket.BinaryMessage, request); err != nil {
		t.Fatalf("write document message: %v", err)
	}
	senderReply := readOpenIMJSONFrame(t, sender, 20*time.Second)
	if senderReply["reqIdentifier"] != float64(1003) || senderReply["errCode"] != float64(0) {
		t.Fatalf("OpenIM document sender response failed: %#v", senderReply)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for document message push")
		}
		push := readOpenIMJSONFrame(t, receiver, time.Until(deadline))
		if push["reqIdentifier"] != float64(2001) {
			continue
		}
		encoded, ok := push["data"].(string)
		if !ok {
			t.Fatalf("document push has no data: %#v", push)
		}
		decoded, err := decodeBase64(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(decoded, []byte(`"sourceUrl"`)) || !bytes.Contains(decoded, []byte("document-e2e.pdf")) || !bytes.Contains(decoded, []byte(openIMString(accessData, "url"))) {
			t.Fatalf("receiver push did not contain the uploaded file element: %#v", push)
		}
		return
	}
}

func openIMDataMap(t *testing.T, envelope map[string]any, operation string) map[string]any {
	t.Helper()
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("OpenIM %s response has no data object: %#v", operation, envelope)
	}
	return data
}

func openIMNumber(t *testing.T, data map[string]any, key string) float64 {
	t.Helper()
	value, ok := data[key].(float64)
	if !ok {
		t.Fatalf("OpenIM response field %q is not numeric: %#v", key, data)
	}
	return value
}

func openIMString(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func signedObjectURL(t *testing.T, raw string, shared, part map[string]any) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	for _, values := range []any{shared["query"], part["query"]} {
		applyOpenIMQuery(parsed, values)
	}
	return parsed.String()
}

func applyOpenIMQuery(parsed *url.URL, raw any) {
	items, _ := raw.([]any)
	query := parsed.Query()
	for _, item := range items {
		entry, _ := item.(map[string]any)
		key := openIMString(entry, "key")
		values, _ := entry["values"].([]any)
		for _, value := range values {
			if stringValue, ok := value.(string); ok {
				query.Add(key, stringValue)
			}
		}
	}
	parsed.RawQuery = query.Encode()
}

func applyOpenIMKeyValues(req *http.Request, raw any) {
	items, _ := raw.([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		key := openIMString(entry, "key")
		values, _ := entry["values"].([]any)
		for _, value := range values {
			if stringValue, ok := value.(string); ok {
				req.Header.Add(key, stringValue)
			}
		}
	}
}

func decodeBase64(value string) ([]byte, error) {
	// Keep the import and wire handling in one place; the production auth E2E
	// already uses the same standard encoding for WebSocket push frames.
	return base64.StdEncoding.DecodeString(value)
}
