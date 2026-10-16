package httpapi

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"pingmessenger/internal/stories"
)

type storyURLTestStore struct {
	mu       sync.Mutex
	presigns int
}

func (s *storyURLTestStore) Put(context.Context, string, string, []byte) error { return nil }
func (s *storyURLTestStore) Delete(context.Context, string) error              { return nil }
func (s *storyURLTestStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.presigns++
	return fmt.Sprintf("https://private.example/%s?X-Amz-Signature=%d", key, s.presigns), nil
}

func (s *storyURLTestStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.presigns
}

func TestSignStoryMediaReusesAndEvictsURL(t *testing.T) {
	store := &storyURLTestStore{}
	a := &API{objects: store, storyURLs: make(map[string]storyURLCacheEntry)}
	first := &stories.Story{StorageKey: "stories/user/story"}
	second := &stories.Story{StorageKey: first.StorageKey}
	if err := a.signStoryMedia(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := a.signStoryMedia(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if first.MediaURL == "" || first.MediaURL != second.MediaURL || store.count() != 1 {
		t.Fatalf("cache did not reuse signed URL: first=%q second=%q calls=%d", first.MediaURL, second.MediaURL, store.count())
	}

	a.evictStoryMediaURL(first.StorageKey)
	third := &stories.Story{StorageKey: first.StorageKey}
	if err := a.signStoryMedia(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	if third.MediaURL == first.MediaURL || store.count() != 2 {
		t.Fatalf("eviction did not force a new URL: first=%q third=%q calls=%d", first.MediaURL, third.MediaURL, store.count())
	}

	a.storyURLMu.Lock()
	a.storyURLs[first.StorageKey] = storyURLCacheEntry{URL: third.MediaURL, ExpiresAt: time.Now().Add(-time.Second)}
	a.storyURLMu.Unlock()
	fourth := &stories.Story{StorageKey: first.StorageKey}
	if err := a.signStoryMedia(context.Background(), fourth); err != nil {
		t.Fatal(err)
	}
	if fourth.MediaURL == third.MediaURL || store.count() != 3 {
		t.Fatalf("expired URL was not refreshed: third=%q fourth=%q calls=%d", third.MediaURL, fourth.MediaURL, store.count())
	}
}

func TestSignStoryMediaRequiresPrivateSigner(t *testing.T) {
	a := &API{objects: &storyURLOnlyStore{}, storyURLs: make(map[string]storyURLCacheEntry)}
	if err := a.signStoryMedia(context.Background(), &stories.Story{StorageKey: "stories/user/story"}); err == nil {
		t.Fatal("expected an error when the object store cannot sign private URLs")
	}
}

func TestEvictExpiredStoryURLsBoundsExpiredEntries(t *testing.T) {
	a := &API{storyURLs: map[string]storyURLCacheEntry{
		"expired": {URL: "old", ExpiresAt: time.Now().Add(-time.Second)},
		"live":    {URL: "new", ExpiresAt: time.Now().Add(time.Minute)},
	}}
	a.storyURLMu.Lock()
	a.evictExpiredStoryURLsLocked(time.Now())
	a.storyURLMu.Unlock()
	if _, ok := a.storyURLs["expired"]; ok {
		t.Fatal("expired story URL was not evicted")
	}
	if _, ok := a.storyURLs["live"]; !ok {
		t.Fatal("live story URL was evicted")
	}
}

func TestValidStoryOriginRequiresExactHost(t *testing.T) {
	tests := []struct {
		origin string
		valid  bool
	}{
		{"", true},
		{"https://api.example.test", true},
		{"https://evil.example.test/api.example.test", false},
		{"https://api.example.test.evil.test", false},
		{"https://api.example.test/path", false},
		{"https://api.example.test?redirect=evil", false},
	}
	for _, tt := range tests {
		if got := validStoryOrigin(tt.origin, "api.example.test"); got != tt.valid {
			t.Errorf("validStoryOrigin(%q)=%v, want %v", tt.origin, got, tt.valid)
		}
	}
}

type storyURLOnlyStore struct{}

func (storyURLOnlyStore) Put(context.Context, string, string, []byte) error { return nil }
func (storyURLOnlyStore) Delete(context.Context, string) error              { return nil }
