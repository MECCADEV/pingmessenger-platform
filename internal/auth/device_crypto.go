package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// DeviceTokenCipher keeps the short-lived OpenIM token recoverable only by the
// server. This is necessary for exact remote revocation; clients still see a
// token only at issue time. The key must be an independent 32-byte secret.
type DeviceTokenCipher struct{ aead cipher.AEAD }

func NewDeviceTokenCipher(encodedKey string) (*DeviceTokenCipher, error) {
	key, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("DEVICE_TOKEN_ENCRYPTION_KEY must be a base64url-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &DeviceTokenCipher{aead: aead}, nil
}

func (c *DeviceTokenCipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(nonce, c.aead.Seal(nil, nonce, []byte(plain), nil)...)), nil
}
func (c *DeviceTokenCipher) Decrypt(encoded string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(b) < c.aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted device token")
	}
	plain, err := c.aead.Open(nil, b[:c.aead.NonceSize()], b[c.aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt device token: %w", err)
	}
	return string(plain), nil
}
func Fingerprint(value string) string {
	s := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(s[:])
}
