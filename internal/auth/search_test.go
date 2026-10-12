package auth

import "testing"

func TestEscapeSearchLike(t *testing.T) {
	if got, want := escapeSearchLike(`a%_\\b`), `a\%\_\\\\b`; got != want {
		t.Fatalf("escapeSearchLike() = %q, want %q", got, want)
	}
}
