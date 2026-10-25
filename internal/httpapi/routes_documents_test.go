package httpapi

import "testing"

func TestSafeDocumentName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"report.pdf", "report.pdf"},
		{"nested/report.pdf", "report.pdf"},
		{"", ""},
		{"../secrets", "secrets"},
		{"bad\nname.pdf", ""},
	}
	for _, tt := range tests {
		if got := safeDocumentName(tt.name); got != tt.want {
			t.Errorf("safeDocumentName(%q)=%q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDocumentUploadFingerprintCoversNameTypeAndBytes(t *testing.T) {
	base, err := hashDocumentUploadRequest("report.pdf", "application/pdf", []byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []struct {
		name, contentType string
		data              []byte
	}{
		{"renamed.pdf", "application/pdf", []byte("content")},
		{"report.pdf", "application/zip", []byte("content")},
		{"report.pdf", "application/pdf", []byte("changed")},
	} {
		fingerprint, hashErr := hashDocumentUploadRequest(changed.name, changed.contentType, changed.data)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		if fingerprint == base {
			t.Errorf("changed upload request produced the same fingerprint: %+v", changed)
		}
	}
	if _, err = hashDocumentUploadRequest("report.pdf", " APPLICATION/PDF ", []byte("content")); err != nil {
		t.Fatalf("normalized MIME fingerprint: %v", err)
	}
}

func TestDocumentUploadIdempotencyKeyValidation(t *testing.T) {
	for _, key := range []string{"", "local-id-123", "32-byte-hex-idempotency-key-00001"} {
		if got, err := parseStoryIdempotencyKey(key); err != nil || got != key {
			t.Errorf("parse idempotency key %q = %q, %v", key, got, err)
		}
	}
	for _, key := range []string{"has space", "line\nbreak", string(make([]byte, 129))} {
		if _, err := parseStoryIdempotencyKey(key); err == nil {
			t.Errorf("parse idempotency key %q succeeded, want error", key)
		}
	}
}
