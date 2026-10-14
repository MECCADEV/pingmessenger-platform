package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/fasthttp/websocket"
	json "github.com/goccy/go-json"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/payments"
)

type createPaymentRequest struct {
	PaymentID     string `json:"payment_id" validate:"required,max=128"`
	ToUserID      string `json:"to_user_id" validate:"required,uuid4"`
	PaymentType   string `json:"payment_type" validate:"required,oneof=direct fund_campaign"`
	ChainID       string `json:"chain_id" validate:"required,max=64"`
	TransactionID string `json:"transaction_id" validate:"required,max=256"`
	Asset         string `json:"asset" validate:"omitempty,max=64"`
	AmountAtomic  string `json:"amount_atomic" validate:"required,max=80,numeric"`
	ExpiresInSec  int    `json:"expires_in_seconds" validate:"omitempty,min=30,max=86400"`
}

func (a *API) registerPaymentRoutes(r *router.Router) {
	r.POST("/v1/conversations/{conversationID}/payments", a.createPayment)
	r.GET("/v1/conversations/{conversationID}/payments", a.listPayments)
	r.GET("/v1/conversations/{conversationID}/payments/sync", a.syncPayments)
	r.GET("/v1/conversations/{conversationID}/payments/ws", a.paymentWebSocket)
	r.GET("/v1/payments/{paymentID}", a.getPayment)
	r.GET("/v1/payments/{paymentID}/chain", a.getPaymentChain)
	r.POST("/v1/internal/payments/events", a.applyPaymentEvent)
}

func conversationID(ctx *fasthttp.RequestCtx) string {
	return strings.TrimSpace(ctx.UserValue("conversationID").(string))
}

func (a *API) createPayment(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req createPaymentRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	expires := 120 * time.Second
	if req.ExpiresInSec > 0 {
		expires = time.Duration(req.ExpiresInSec) * time.Second
	}
	p, err := a.payments.Create(context.Background(), payments.CreateRequest{
		PaymentID: req.PaymentID, ConversationID: conversationID(ctx), FromUserID: userID, ToUserID: req.ToUserID,
		PaymentType: req.PaymentType, ChainID: req.ChainID, TransactionID: req.TransactionID, Asset: req.Asset,
		AmountAtomic: req.AmountAtomic, ExpiresAt: time.Now().Add(expires),
	})
	if err == payments.ErrConflict {
		writeJSON(ctx, http.StatusConflict, map[string]string{"error": "payment_id already identifies a different payment"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusBadRequest, map[string]string{"error": "could not create payment"})
		return
	}
	messageStatus := "pending"
	if err := a.emitPaymentMessage(context.Background(), p, payments.PaymentCreatedKind); err == nil {
		messageStatus = "sent"
	} else {
		log.Printf("payment creation message projection failed payment=%s: %v", p.PaymentID, err)
	}
	a.indexChainAsync(p.ChainID, p.TransactionID)
	writeJSON(ctx, http.StatusCreated, map[string]any{"payment": p, "watch_status": "pending_adapter", "message_status": messageStatus})
}

func (a *API) listPayments(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 50
	if raw := string(ctx.QueryArgs().Peek("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	to := string(ctx.QueryArgs().Peek("to"))
	if to == "latest" {
		to = ""
	}
	lastKnown := string(ctx.QueryArgs().Peek("last_known_payment_id"))
	if lastKnown == "" {
		lastKnown = string(ctx.QueryArgs().Peek("lastKnownPaymentId"))
	}
	lastBlock := string(ctx.QueryArgs().Peek("last_block_id"))
	if lastBlock == "" {
		lastBlock = string(ctx.QueryArgs().Peek("lastBlockID"))
	}
	items, err := a.payments.List(context.Background(), conversationID(ctx), userID, to, lastKnown, lastBlock, limit)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not list payments"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"payments": items, "next_to": nextPaymentCursor(items), "next_block_id": nextPaymentBlockCursor(items)})
}

func (a *API) syncPayments(ctx *fasthttp.RequestCtx) {
	a.listPayments(ctx)
}

func (a *API) getPayment(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	paymentID := strings.TrimSpace(ctx.UserValue("paymentID").(string))
	payment, err := a.payments.Get(context.Background(), paymentID)
	if errors.Is(err, payments.ErrNotFound) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load payment"})
		return
	}
	if payment.FromUserID != userID && payment.ToUserID != userID {
		writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "payment is not visible to this user"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"payment": payment})
}

// indexChainAsync deliberately runs after a payment intent or watcher update
// has been accepted. A transaction may not be visible at creation time; the
// later status callback gives the chain RPC another opportunity to resolve it.
func (a *API) indexChainAsync(chainID, transactionID string) {
	if a.chainIndexer == nil {
		return
	}
	go func() {
		if _, err := a.chainIndexer.Index(context.Background(), chainID, transactionID); err != nil {
			log.Printf("chain transaction indexing failed chain=%s tx=%s: %v", chainID, transactionID, err)
		}
	}()
}

func (a *API) getPaymentChain(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	paymentID := strings.TrimSpace(ctx.UserValue("paymentID").(string))
	payment, err := a.payments.Get(context.Background(), paymentID)
	if errors.Is(err, payments.ErrNotFound) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load payment"})
		return
	}
	if payment.FromUserID != userID && payment.ToUserID != userID {
		writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "payment is not visible to this user"})
		return
	}
	if a.chainIndexer == nil {
		writeJSON(ctx, http.StatusServiceUnavailable, map[string]string{"error": "chain indexer is not configured"})
		return
	}
	projection, err := a.chainIndexer.GetForPayment(context.Background(), paymentID)
	if err != nil {
		a.indexChainAsync(payment.ChainID, payment.TransactionID)
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "chain transaction is not indexed"})
		return
	}
	writeJSON(ctx, http.StatusOK, projection)
}

func nextPaymentCursor(items []*payments.Payment) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1].PaymentID
}

func nextPaymentBlockCursor(items []*payments.Payment) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1].BlockID
}

// applyPaymentEvent is private service-to-service ingress for PingPay-user.
// The body is authenticated before decoding so a caller cannot smuggle a
// second representation of the signed event through JSON normalization.
func (a *API) applyPaymentEvent(ctx *fasthttp.RequestCtx) {
	if a.paymentEventSecret == "" {
		writeJSON(ctx, http.StatusServiceUnavailable, map[string]string{"error": "payment event ingress is not configured"})
		return
	}
	signature, err := hex.DecodeString(strings.TrimSpace(string(ctx.Request.Header.Peek("X-PingPay-Signature"))))
	if err != nil || !validPaymentEventSignature(ctx.PostBody(), signature, a.paymentEventSecret) {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid payment event signature"})
		return
	}
	var update payments.PaymentUpdate
	if err := json.Unmarshal(ctx.PostBody(), &update); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid payment event"})
		return
	}
	payment, err := a.payments.ApplyUpdate(context.Background(), update)
	switch {
	case err == nil:
		a.indexChainAsync(payment.ChainID, payment.TransactionID)
		a.emitPaymentMessageAsync(payment, payments.PaymentUpdatedKind)
		writeJSON(ctx, http.StatusOK, map[string]any{"payment": payment})
	case errors.Is(err, payments.ErrNotFound):
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "payment not found"})
	case errors.Is(err, payments.ErrConflict):
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "payment event does not match its transaction"})
	case errors.Is(err, payments.ErrStaleUpdate), errors.Is(err, payments.ErrExpired):
		if errors.Is(err, payments.ErrExpired) && payment != nil {
			a.indexChainAsync(payment.ChainID, payment.TransactionID)
			a.emitPaymentMessageAsync(payment, payments.PaymentUpdatedKind)
		}
		writeJSON(ctx, http.StatusConflict, map[string]any{"error": err.Error(), "payment": payment})
	case errors.Is(err, payments.ErrInvalidRequest):
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid payment event"})
	default:
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not apply payment event"})
	}
}

func validPaymentEventSignature(body, signature []byte, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(signature, mac.Sum(nil))
}

func (a *API) paymentWebSocket(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	conversation := conversationID(ctx)
	// The stream may be opened before the first payment exists. Subscriber
	// filtering below ensures an authenticated user only receives events for
	// payments where they are the sender or recipient; the HTTP list remains the
	// source of truth for historical authorization.
	upgrader := websocket.FastHTTPUpgrader{HandshakeTimeout: 10 * time.Second, CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
		origin := strings.TrimSpace(string(ctx.Request.Header.Peek("Origin")))
		return origin == "" || strings.Contains(origin, string(ctx.Host()))
	}}
	if err := upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
		defer conn.Close()
		conn.SetReadLimit(4096)
		updates, unsubscribe := a.payments.Subscribe(conversation, userID)
		defer unsubscribe()
		// Subscribe before taking the snapshot so an update that races the
		// snapshot is queued and cannot be lost. Clients deduplicate by version.
		if snapshot, listErr := a.payments.List(context.Background(), conversation, userID, "", "", "", 100); listErr == nil {
			for _, payment := range snapshot {
				if err := conn.WriteJSON(map[string]any{"type": "payment.updated", "payment": payment}); err != nil {
					return
				}
			}
		}
		closed := make(chan struct{})
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					close(closed)
					return
				}
			}
		}()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case p, open := <-updates:
				if !open {
					return
				}
				if err := conn.WriteJSON(map[string]any{"type": "payment.updated", "payment": p}); err != nil {
					return
				}
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			case <-closed:
				return
			}
		}
	}); err != nil {
		return
	}
}
