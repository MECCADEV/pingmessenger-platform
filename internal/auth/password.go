package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HashPassword derives an Argon2id password verifier. Only the resulting
// encoded verifier may reach the repository; plaintext stays in the handler.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return fmt.Sprintf("argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword checks an Argon2id verifier created by HashPassword. Invalid
// encoded values are credentials failures, never reasons to panic or expose
// parsing detail to an API client.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	params := strings.Split(parts[2], ",")
	if len(params) != 3 {
		return false
	}
	var memory, iterations uint64
	var parallelism uint64
	if _, err := fmt.Sscanf(params[0], "m=%d", &memory); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(params[1], "t=%d", &iterations); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(params[2], "p=%d", &parallelism); err != nil || memory == 0 || iterations == 0 || parallelism == 0 || memory > 1<<31 || iterations > 255 || parallelism > 255 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) < 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(expected) == 0 {
		return false
	}
	derived := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(derived, expected) == 1
}
