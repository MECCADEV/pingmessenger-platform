package auth

import "testing"

func TestVerifyPassword(t *testing.T) {
	encoded, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct-horse-battery-staple", encoded) {
		t.Fatal("correct password was rejected")
	}
	if VerifyPassword("wrong-password", encoded) {
		t.Fatal("wrong password was accepted")
	}
	if VerifyPassword("correct-horse-battery-staple", "not-an-argon2-verifier") {
		t.Fatal("malformed verifier was accepted")
	}
}
