package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"time"
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

// NewTOTPSecret returns an RFC 6238 compatible Base32 secret.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// TOTPMatches accepts one adjacent 30-second period to tolerate small device
// clock drift. It is compatible with standard authenticator apps.
func TOTPMatches(secret, code string, now time.Time) bool {
	if len(code) != 6 {
		return false
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) == 0 {
		return false
	}
	for offset := int64(-1); offset <= 1; offset++ {
		counter := uint64(now.Unix()/30 + offset)
		buf := make([]byte, 8)
		for i := 7; i >= 0; i-- {
			buf[i] = byte(counter)
			counter >>= 8
		}
		mac := hmac.New(sha1.New, key)
		_, _ = mac.Write(buf)
		sum := mac.Sum(nil)
		i := sum[len(sum)-1] & 15
		value := (uint32(sum[i])&127)<<24 | uint32(sum[i+1])<<16 | uint32(sum[i+2])<<8 | uint32(sum[i+3])
		if hmac.Equal([]byte(fmt.Sprintf("%06d", value%1000000)), []byte(code)) {
			return true
		}
	}
	return false
}
