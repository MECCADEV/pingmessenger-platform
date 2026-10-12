package payments

import (
	"context"
	"sync"
)

type Service struct {
	repo *Repository
	mu   sync.RWMutex
	hubs map[string]map[chan *Payment]struct{}
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, hubs: make(map[string]map[chan *Payment]struct{})}
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (*Payment, error) {
	p, err := s.repo.Create(ctx, req)
	if err == nil {
		s.publish(p)
	}
	return p, err
}

func (s *Service) Get(ctx context.Context, paymentID string) (*Payment, error) {
	return s.repo.Get(ctx, paymentID)
}

func (s *Service) List(ctx context.Context, conversationID, userID, toID, lastKnownID string, limit int) ([]*Payment, error) {
	return s.repo.List(ctx, conversationID, userID, toID, lastKnownID, limit)
}

func (s *Service) ApplyUpdate(ctx context.Context, update PaymentUpdate) (*Payment, error) {
	p, err := s.repo.ApplyUpdate(ctx, update)
	if p != nil && (err == nil || err == ErrExpired) {
		s.publish(p)
	}
	return p, err
}

// StartWatcher attaches one payment to a PingPay-user stream. It is deliberately
// opt-in: the platform can boot and serve pending records while PingPay-user is
// unavailable, then attach the watcher when service discovery is healthy.
func (s *Service) StartWatcher(ctx context.Context, watcher TransactionWatcher, req WatchRequest) error {
	req.Action = "watch"
	updates, err := watcher.Watch(ctx, req)
	if err != nil {
		return err
	}
	go func() {
		defer func() {
			_ = watcher.Unwatch(context.Background(), WatchRequest{Action: "unwatch", PaymentID: req.PaymentID, ChainID: req.ChainID, TransactionID: req.TransactionID})
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case update, open := <-updates:
				if !open {
					return
				}
				_, _ = s.ApplyUpdate(context.Background(), update)
			}
		}
	}()
	return nil
}

// Subscribe returns a bounded channel. A slow WebSocket client may miss an
// update, but it can always recover the authoritative state via the HTTP list
// endpoint; this keeps the service from being blocked by disconnected clients.
func (s *Service) Subscribe(conversationID string) (<-chan *Payment, func()) {
	ch := make(chan *Payment, 32)
	s.mu.Lock()
	if s.hubs[conversationID] == nil {
		s.hubs[conversationID] = make(map[chan *Payment]struct{})
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

func (s *Service) publish(p *Payment) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.hubs[p.ConversationID] {
		select {
		case ch <- p:
		default:
		}
	}
}
