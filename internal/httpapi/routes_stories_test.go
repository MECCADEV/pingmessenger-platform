package httpapi

import (
	"errors"
	"testing"
)

func TestParseStoryIDsAcceptsJSONAndCommaForms(t *testing.T) {
	for _, raw := range []string{`["11111111-1111-1111-1111-111111111111","22222222-2222-2222-2222-222222222222"]`, "11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222"} {
		ids, err := parseStoryIDs(raw)
		if err != nil || len(ids) != 2 {
			t.Fatalf("raw=%q ids=%v err=%v", raw, ids, err)
		}
	}
}

func TestParseStoryIDsRejectsMalformedIDs(t *testing.T) {
	if _, err := parseStoryIDs(`["]`); err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if _, err := parseStoryIDs("not-a-uuid"); err == nil {
		t.Fatal("expected invalid ID error")
	}
}

func TestStoryMediaTypeAcceptsImagesAndVideosOnly(t *testing.T) {
	tests := []struct {
		contentType string
		want        string
		ok          bool
	}{
		{contentType: "image/png", want: "image", ok: true},
		{contentType: "VIDEO/MP4", want: "video", ok: true},
		{contentType: "image/", ok: false},
		{contentType: "application/octet-stream", ok: false},
		{contentType: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := storyMediaType(tt.contentType)
		if ok != tt.ok || string(got) != tt.want {
			t.Errorf("storyMediaType(%q)=(%q,%v), want (%q,%v)", tt.contentType, got, ok, tt.want, tt.ok)
		}
	}
}

func TestValidStoryBackgroundColor(t *testing.T) {
	for _, value := range []string{"#8619F5", "#000000", "#abcdef"} {
		if !validStoryBackgroundColor(value) {
			t.Errorf("expected %q to be valid", value)
		}
	}
	for _, value := range []string{"", "8619F5", "#12345", "#1234567", "#12GG56"} {
		if validStoryBackgroundColor(value) {
			t.Errorf("expected %q to be invalid", value)
		}
	}
}

func TestParseStoryIdempotencyKey(t *testing.T) {
	for _, key := range []string{"", "draft-01J0ABCXYZ", "0123456789abcdef"} {
		got, err := parseStoryIdempotencyKey(key)
		if err != nil || got != key {
			t.Fatalf("parseStoryIdempotencyKey(%q)=(%q,%v)", key, got, err)
		}
	}
	for _, key := range []string{"bad key", " key ", "bad\nkey", string(make([]byte, 129))} {
		if _, err := parseStoryIdempotencyKey(key); !errors.Is(err, errInvalidIdempotencyKey) {
			t.Errorf("parseStoryIdempotencyKey(%q) error=%v", key, err)
		}
	}
}

func TestStoryCreateFingerprintCanonicalizesVisibilityIDsAndDetectsChanges(t *testing.T) {
	first, err := hashStoryCreateRequest("image", "IMAGE/PNG", []byte("media"), "caption", "", "", "contacts", []string{"b", "a"}, 86400)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := hashStoryCreateRequest("image", "image/png", []byte("media"), "caption", "", "", "contacts", []string{"a", "b"}, 86400)
	if err != nil {
		t.Fatal(err)
	}
	if first != reordered {
		t.Fatal("semantically identical story request produced different fingerprints")
	}
	changed, err := hashStoryCreateRequest("image", "image/png", []byte("other media"), "caption", "", "", "contacts", []string{"a", "b"}, 86400)
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different media bytes reused the same fingerprint")
	}
}
