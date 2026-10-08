package auth

import (
	"testing"
	"time"
)

func TestTOTPMatchesRFC6238SixDigits(t *testing.T) {
	// RFC 6238 SHA-1 secret at 59 seconds, represented as a six-digit code.
	if !TOTPMatches("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "287082", time.Unix(59, 0)) {
		t.Fatal("expected RFC-compatible TOTP code to verify")
	}
	if TOTPMatches("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "000000", time.Unix(59, 0)) {
		t.Fatal("incorrect TOTP code accepted")
	}
}
