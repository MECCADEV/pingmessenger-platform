package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

var errInvalidIdempotencyKey = errors.New("invalid Idempotency-Key")

func parseStoryIdempotencyKey(raw string) (string, error) {
	key := raw
	if key == "" {
		return "", nil
	}
	if len(key) > 128 {
		return "", errInvalidIdempotencyKey
	}
	for _, value := range []byte(key) {
		if value < 0x21 || value > 0x7e {
			return "", errInvalidIdempotencyKey
		}
	}
	return key, nil
}

type storyCreateFingerprint struct {
	MediaType       string   `json:"media_type"`
	ContentType     string   `json:"content_type"`
	MediaSHA256     string   `json:"media_sha256"`
	Caption         string   `json:"caption"`
	Text            string   `json:"text"`
	BackgroundColor string   `json:"background_color"`
	Visibility      string   `json:"visibility"`
	VisibilityUsers []string `json:"visibility_users"`
	ExpiresIn       int64    `json:"expires_in_seconds"`
}

func hashStoryCreateRequest(mediaType, contentType string, data []byte, caption, text, backgroundColor, visibility string, userIDs []string, expiresIn int64) (string, error) {
	mediaHash := sha256.Sum256(data)
	users := append([]string(nil), userIDs...)
	sort.Strings(users)
	canonical, err := json.Marshal(storyCreateFingerprint{
		MediaType:       mediaType,
		ContentType:     strings.ToLower(strings.TrimSpace(contentType)),
		MediaSHA256:     hex.EncodeToString(mediaHash[:]),
		Caption:         caption,
		Text:            text,
		BackgroundColor: strings.ToUpper(backgroundColor),
		Visibility:      visibility,
		VisibilityUsers: users,
		ExpiresIn:       expiresIn,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
