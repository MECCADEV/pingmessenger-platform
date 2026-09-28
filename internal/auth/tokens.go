package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type TokenManager struct {
	secret, issuer        string
	accessTTL, refreshTTL time.Duration
}

func NewTokenManager(secret, issuer string, access, refresh time.Duration) *TokenManager {
	return &TokenManager{secret: secret, issuer: issuer, accessTTL: access, refreshTTL: refresh}
}
func (m *TokenManager) IssueAccess(userID, sessionID string) (string, time.Time, error) {
	exp := time.Now().Add(m.accessTTL)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{"sub": userID, "sid": sessionID, "iss": m.issuer, "exp": exp.Unix(), "iat": time.Now().Unix()})
	if err != nil {
		return "", time.Time{}, err
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + m.sign(body), exp, nil
}
func (m *TokenManager) NewRefresh() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, m.Hash(raw), nil
}
func (m *TokenManager) Hash(token string) string {
	h := hmac.New(sha256.New, []byte(m.secret))
	_, _ = h.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (m *TokenManager) sign(body string) string { return m.Hash(body) }
func (m *TokenManager) VerifyAccess(token string) (map[string]any, error) {
	p := strings.Split(token, ".")
	if len(p) != 3 || !hmac.Equal([]byte(m.sign(p[0]+"."+p[1])), []byte(p[2])) {
		return nil, fmt.Errorf("invalid token")
	}
	b, err := base64.RawURLEncoding.DecodeString(p[1])
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c["iss"] != m.issuer {
		return nil, fmt.Errorf("invalid issuer")
	}
	if exp, ok := c["exp"].(float64); !ok || time.Now().Unix() >= int64(exp) {
		return nil, fmt.Errorf("expired token")
	}
	return c, nil
}
func (m *TokenManager) RefreshTTL() time.Duration { return m.refreshTTL }
