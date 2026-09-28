package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func NewOTP() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", (uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]))%1000000), nil
}
func HashOTP(secret, code string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(code))
	return hex.EncodeToString(h.Sum(nil))
}
func OTPMatches(secret, code, hash string) bool {
	return hmac.Equal([]byte(HashOTP(secret, code)), []byte(hash))
}
