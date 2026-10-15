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

type storyURLOnlyStore struct{}

func (storyURLOnlyStore) Put(context.Context, string, string, []byte) error { return nil }
func (storyURLOnlyStore) Delete(context.Context, string) error              { return nil }
