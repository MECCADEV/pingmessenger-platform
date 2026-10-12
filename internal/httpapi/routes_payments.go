package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	r.GET("/v1/conversations/{conversationID}/payments/ws", a.paymentWebSocket)
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
	writeJSON(ctx, http.StatusCreated, map[string]any{"payment": p, "watch_status": "pending_adapter"})
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
	items, err := a.payments.List(context.Background(), conversationID(ctx), userID, to, lastKnown, limit)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not list payments"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"payments": items, "next_to": nextPaymentCursor(items)})
}

func nextPaymentCursor(items []*payments.Payment) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1].PaymentID
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
	mac := hmac.New(sha256.New, []byte(a.paymentEventSecret))
	mac.Write(ctx.PostBody())
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
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
		writeJSON(ctx, http.StatusOK, map[string]any{"payment": payment})
	case errors.Is(err, payments.ErrNotFound):
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "payment not found"})
	case errors.Is(err, payments.ErrConflict):
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "payment event does not match its transaction"})
	case errors.Is(err, payments.ErrStaleUpdate), errors.Is(err, payments.ErrExpired):
		writeJSON(ctx, http.StatusConflict, map[string]any{"error": err.Error(), "payment": payment})
	case errors.Is(err, payments.ErrInvalidRequest):
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid payment event"})
	default:
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not apply payment event"})
	}
}

func (a *API) paymentWebSocket(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	conversation := conversationID(ctx)
	// Authorize the upgrade by requiring at least one visible record. Clients
	// without history use the HTTP create/list path first; this avoids exposing
	// an unauthenticated conversation stream.
	items, err := a.payments.List(context.Background(), conversation, userID, "", "", 1)
	if err != nil || len(items) == 0 {
		writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "conversation is not available"})
		return
	}
	upgrader := websocket.FastHTTPUpgrader{HandshakeTimeout: 10 * time.Second, CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
		origin := strings.TrimSpace(string(ctx.Request.Header.Peek("Origin")))
		return origin == "" || strings.Contains(origin, string(ctx.Host()))
	}}
	if err := upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
		defer conn.Close()
		updates, unsubscribe := a.payments.Subscribe(conversation)
		defer unsubscribe()
		for {
			select {
			case p, open := <-updates:
				if !open {
					return
				}
				if err := conn.WriteJSON(map[string]any{"type": "payment.updated", "payment": p}); err != nil {
					return
				}
			case <-time.After(30 * time.Second):
				if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
					return
				}
			}
		}
	}); err != nil {
		return
	}
}
