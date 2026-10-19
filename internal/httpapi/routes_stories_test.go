package httpapi

import "testing"

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
