package httpapi

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"pingmessenger/internal/stories"
)

// storySubscription is created for every authenticated story WebSocket. A
// per-connection write lock is required because the read loop and the
// PostgreSQL event listener can write to the same socket concurrently.
type storySubscription struct {
	userID string
	conn   *websocket.Conn
	write  sync.Mutex
}

func (s *storySubscription) send(message any) error {
	s.write.Lock()
	defer s.write.Unlock()
	return s.conn.WriteJSON(message)
}

func (a *API) addStorySubscription(sub *storySubscription) bool {
	a.storySubsMu.Lock()
	defer a.storySubsMu.Unlock()
	if a.storySubs == nil {
		a.storySubs = make(map[string]map[*storySubscription]struct{})
	}
	if a.storySubs[sub.userID] == nil {
		a.storySubs[sub.userID] = make(map[*storySubscription]struct{})
	}
	if len(a.storySubs[sub.userID]) >= 8 {
		return false
	}
	a.storySubs[sub.userID][sub] = struct{}{}
	return true
}

func (a *API) removeStorySubscription(sub *storySubscription) {
	a.storySubsMu.Lock()
	defer a.storySubsMu.Unlock()
	clients := a.storySubs[sub.userID]
	delete(clients, sub)
	if len(clients) == 0 {
		delete(a.storySubs, sub.userID)
	}
}

func (a *API) broadcastStoryEvent(ctx context.Context, eventID int64) {
	delivery, err := a.stories.EventDelivery(ctx, eventID)
	if err != nil {
		log.Printf("story event %d lookup failed: %v", eventID, err)
		return
	}
	if err := a.signStoryEvents(ctx, []*stories.Event{delivery.Event}); err != nil {
		log.Printf("story event %d media signing failed: %v", eventID, err)
		return
	}
	message := map[string]any{
		"type":          "stories.event",
		"event":         delivery.Event,
		"global_cursor": eventID,
	}
	a.storySubsMu.RLock()
	clients := make([]*storySubscription, 0, len(a.storySubs[delivery.RecipientID]))
	for client := range a.storySubs[delivery.RecipientID] {
		clients = append(clients, client)
	}
	a.storySubsMu.RUnlock()
	for _, client := range clients {
		if err := client.send(message); err != nil {
			a.removeStorySubscription(client)
		}
	}
}

// RunStoryEvents listens on PostgreSQL so every API replica broadcasts the
// same committed story event to its local WebSocket clients. The durable
// story_events table remains the recovery source for clients that reconnect.
func (a *API) RunStoryEvents(ctx context.Context) {
	for {
		err := a.stories.ListenStoryEvents(ctx, a.broadcastStoryEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("story event listener stopped: %v", err)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
