package disappearing

import (
	"testing"
	"time"
)

func TestSubscribePublishAndUnsubscribe(t *testing.T) {
	s := &Service{hubs: make(map[string]map[chan Event]struct{})}
	updates, unsubscribe := s.Subscribe("conversation-1")
	s.publish(Event{Type: "disappearing.updated", ConversationID: "conversation-1", UserID: "user-1", DurationSeconds: 900, Enabled: true})
	select {
	case event := <-updates:
		if event.DurationSeconds != 900 || !event.Enabled {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("notification was not published")
	}
	unsubscribe()
	if _, ok := <-updates; ok {
		t.Fatal("subscription channel was not closed")
	}
}

func TestPublishDoesNotBlockSlowSubscriber(t *testing.T) {
	s := &Service{hubs: make(map[string]map[chan Event]struct{})}
	updates, unsubscribe := s.Subscribe("conversation-1")
	defer unsubscribe()
	for i := 0; i < cap(updates)+10; i++ {
		s.publish(Event{Type: "disappearing.updated", ConversationID: "conversation-1"})
	}
}
