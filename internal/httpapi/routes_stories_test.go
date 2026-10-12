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
