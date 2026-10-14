package disappearing

import (
	"context"
	"sync"
)

type Service struct {
	repo *Repository
	mu   sync.RWMutex
	hubs map[string]map[chan Event]struct{}
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, hubs: make(map[string]map[chan Event]struct{})}
}

func (s *Service) Get(ctx context.Context, userID, conversationID string) (*Setting, error) {
	return s.repo.Get(ctx, userID, conversationID)
}

func (s *Service) Set(ctx context.Context, userID, conversationID string, durationSeconds int, enabled bool) (*Setting, error) {
	setting, err := s.repo.Upsert(ctx, userID, conversationID, durationSeconds, enabled)
	if err != nil {
		return nil, err
	}
	s.publish(Event{Type: "disappearing.updated", ConversationID: setting.ConversationID, UserID: setting.UserID, DurationSeconds: setting.DurationSeconds, Enabled: setting.Enabled, UpdatedAt: setting.UpdatedAt})
	return setting, nil
}

func (s *Service) Disable(ctx context.Context, userID, conversationID string) error {
	if err := s.repo.Delete(ctx, userID, conversationID); err != nil {
		return err
	}
	s.publish(Event{Type: "disappearing.updated", ConversationID: conversationID, UserID: userID, DurationSeconds: 0, Enabled: false})
	return nil
}

func (s *Service) Subscribe(conversationID string) (<-chan Event, func()) {
	ch := make(chan Event, 16)
	s.mu.Lock()
	if s.hubs[conversationID] == nil {
		s.hubs[conversationID] = make(map[chan Event]struct{})
	}
	s.hubs[conversationID][ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		if clients := s.hubs[conversationID]; clients != nil {
			delete(clients, ch)
			if len(clients) == 0 {
				delete(s.hubs, conversationID)
			}
		}
		close(ch)
		s.mu.Unlock()
	}
}

func (s *Service) publish(event Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.hubs[event.ConversationID] {
		select {
		case ch <- event:
		default:
		}
	}
}
