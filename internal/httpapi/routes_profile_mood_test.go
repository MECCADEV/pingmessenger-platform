package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMood(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want *string
		bad  bool
	}{
		{name: "set", raw: json.RawMessage(`"working"`), want: stringPointer("working")},
		{name: "clear", raw: json.RawMessage(`null`)},
		{name: "unicode at limit", raw: json.RawMessage(`"` + strings.Repeat("😊", 100) + `"`), want: stringPointer(strings.Repeat("😊", 100))},
		{name: "too long", raw: json.RawMessage(`"` + strings.Repeat("😊", 101) + `"`), bad: true},
		{name: "wrong type", raw: json.RawMessage(`123`), bad: true},
		{name: "missing", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMood(tc.raw)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v, bad=%v", err, tc.bad)
			}
			if tc.bad {
				return
			}
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("mood=%v, want %v", got, tc.want)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }
