package payments

import (
	"context"
	"sync"
	"time"
)

type Service struct {
	repo *Repository
	mu   sync.RWMutex
	hubs map[string]map[*paymentSubscriber]struct{}
}

type paymentSubscriber struct {
	userID string
	ch     chan *Payment
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo, hubs: make(map[string]map[*paymentSubscriber]struct{})}
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

func (s *Service) RecordMessageProjection(ctx context.Context, paymentID, kind string, version int64, serverMsgID, clientMsgID string, sendTime int64) error {
	return s.repo.RecordMessageProjection(ctx, paymentID, kind, version, serverMsgID, clientMsgID, sendTime)
}

func (s *Service) MessageProjectionExists(ctx context.Context, paymentID, kind string, version int64) (bool, error) {
	return s.repo.MessageProjectionExists(ctx, paymentID, kind, version)
}

func (s *Service) List(ctx context.Context, conversationID, userID, toID, lastKnownID, lastBlockID string, limit int) ([]*Payment, error) {
	return s.repo.List(ctx, conversationID, userID, toID, lastKnownID, lastBlockID, limit)
}

func (s *Service) ApplyUpdate(ctx context.Context, update PaymentUpdate) (*Payment, error) {
	p, err := s.repo.ApplyUpdate(ctx, update)
	if p != nil && (err == nil || err == ErrExpired) {
		s.publish(p)
	}
	return p, err
}

// FailStale marks pending transaction records as failed once their explicit
// expiry or the operator-controlled stale-payment timer has elapsed. The
// returned rows have UpdateReason=timeout so HTTP/WebSocket consumers can
// distinguish a timeout from a chain-reported failure.
func (s *Service) FailStale(ctx context.Context, age time.Duration) ([]*Payment, error) {
	payments, err := s.repo.FailStale(ctx, age)
	if err != nil {
		return nil, err
	}
	for _, payment := range payments {
		s.publish(payment)
	}
	return payments, nil
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

// Subscribe registers a participant-specific stream. A conversation can have
// other payment records that do not involve this user; filtering here prevents
// those records from being leaked to a connected client.
func (s *Service) Subscribe(conversationID, userID string) (<-chan *Payment, func()) {
	subscriber := &paymentSubscriber{userID: userID, ch: make(chan *Payment, 32)}
	s.mu.Lock()
	if s.hubs[conversationID] == nil {
		s.hubs[conversationID] = make(map[*paymentSubscriber]struct{})
	}
	s.hubs[conversationID][subscriber] = struct{}{}
	s.mu.Unlock()
	return subscriber.ch, func() {
		s.mu.Lock()
		if clients := s.hubs[conversationID]; clients != nil {
			delete(clients, subscriber)
			if len(clients) == 0 {
				delete(s.hubs, conversationID)
			}
		}
		close(subscriber.ch)
		s.mu.Unlock()
	}
}

func (s *Service) publish(p *Payment) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for subscriber := range s.hubs[p.ConversationID] {
		if subscriber.userID != "" && subscriber.userID != p.FromUserID && subscriber.userID != p.ToUserID {
			continue
		}
		select {
		case subscriber.ch <- p:
		default:
		}
	}
}
