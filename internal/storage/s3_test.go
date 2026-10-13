package storage

import (
	"strings"
	"testing"
)

func TestReadLimitedEnforcesTheSameLimitForMedia(t *testing.T) {
	if got, err := ReadLimited(strings.NewReader("1234"), 4); err != nil || string(got) != "1234" {
		t.Fatalf("within limit: got=%q err=%v", got, err)
	}
	if _, err := ReadLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("expected an over-limit media error")
	}
}
