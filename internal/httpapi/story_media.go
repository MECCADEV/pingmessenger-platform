package httpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"pingmessenger/internal/stories"
)

const (
	// Story URLs are bearer capabilities. Keep the window short enough that a
	// copied URL has limited value, while avoiding a signer call per recipient.
	storyMediaURLTTL     = 10 * time.Minute
	storyMediaURLRefresh = time.Minute
	storyURLCacheMax     = 10000
)

type storyURLCacheEntry struct {
	URL       string
	ExpiresAt time.Time
}

type storyObjectURLSigner interface {
	PresignGet(context.Context, string, time.Duration) (string, error)
}

// signStoryMedia attaches a private, short-lived download URL. The cache is
// keyed by the object key rather than the recipient, so eligible recipients
// on this API replica reuse one signed URL until it is near expiry.
func (a *API) signStoryMedia(ctx context.Context, story *stories.Story) error {
	if story == nil || strings.TrimSpace(story.StorageKey) == "" {
		return nil
	}
	signer, ok := a.objects.(storyObjectURLSigner)
	if !ok {
		return errors.New("story object store does not support private URLs")
	}
	key := story.StorageKey
	now := time.Now()
	a.storyURLMu.Lock()
	defer a.storyURLMu.Unlock()
	if a.storyURLs == nil {
		a.storyURLs = make(map[string]storyURLCacheEntry)
	}
	if entry, ok := a.storyURLs[key]; ok && entry.URL != "" && entry.ExpiresAt.After(now.Add(storyMediaURLRefresh)) {
		story.MediaURL = entry.URL
		return nil
	}
	url, err := signer.PresignGet(ctx, key, storyMediaURLTTL)
	if err != nil {
		return err
	}
	a.evictExpiredStoryURLsLocked(now)
	if _, exists := a.storyURLs[key]; !exists && len(a.storyURLs) >= storyURLCacheMax {
		// Evict the oldest live entry when expiry cleanup did not free enough
		// space. This bounds memory even if S3 signing is faster than expiry.
		var oldestKey string
		var oldest time.Time
		for candidate, entry := range a.storyURLs {
			if oldestKey == "" || entry.ExpiresAt.Before(oldest) {
				oldestKey, oldest = candidate, entry.ExpiresAt
			}
		}
		if oldestKey != "" {
			delete(a.storyURLs, oldestKey)
		}
	}
	a.storyURLs[key] = storyURLCacheEntry{URL: url, ExpiresAt: now.Add(storyMediaURLTTL)}
	story.MediaURL = url
	return nil
}

func (a *API) evictExpiredStoryURLsLocked(now time.Time) {
	for key, entry := range a.storyURLs {
		if !entry.ExpiresAt.After(now) {
			delete(a.storyURLs, key)
		}
	}
}

func (a *API) evictStoryMediaURL(storageKey string) {
	if strings.TrimSpace(storageKey) == "" {
		return
	}
	a.storyURLMu.Lock()
	delete(a.storyURLs, storageKey)
	a.storyURLMu.Unlock()
}

func (a *API) signStoryEvents(ctx context.Context, events []*stories.Event) error {
	for _, event := range events {
		if event == nil || event.Story == nil {
			continue
		}
		if err := a.signStoryMedia(ctx, event.Story); err != nil {
			return err
		}
	}
	return nil
}
