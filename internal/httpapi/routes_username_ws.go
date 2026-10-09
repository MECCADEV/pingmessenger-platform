package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/fasthttp/websocket"
	json "github.com/goccy/go-json"
	"github.com/valyala/fasthttp"
)

type usernameAvailabilityMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Username  string `json:"username"`
}

func (a *API) registerUsernameAvailabilityWS(r *router.Router) {
	r.GET("/v1/auth/username/availability", a.usernameAvailabilityWS)
}

// usernameAvailabilityWS is a small request/response WebSocket protocol. It
// deliberately exposes only availability, never account identifiers or
// metadata. HTTP signup/update remain the authoritative uniqueness operations.
func (a *API) usernameAvailabilityWS(ctx *fasthttp.RequestCtx) {
	upgrader := websocket.FastHTTPUpgrader{
		HandshakeTimeout: 10 * time.Second,
		CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
			origin := strings.TrimSpace(string(ctx.Request.Header.Peek("Origin")))
			return origin == "" || strings.Contains(origin, string(ctx.Host()))
		},
	}
	if err := upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
		defer conn.Close()
		conn.SetReadLimit(4096)
		for count := 0; count < 60; count++ {
			if err := conn.SetReadDeadline(time.Now().Add(2 * time.Minute)); err != nil {
				return
			}
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request usernameAvailabilityMessage
			decoder := json.NewDecoder(bytes.NewReader(payload))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil || request.Type != "check_username" {
				_ = conn.WriteJSON(map[string]any{"type": "error", "request_id": request.RequestID, "code": "invalid_request", "message": "type must be check_username and the JSON must be valid"})
				continue
			}
			request.Username = strings.ToLower(strings.TrimSpace(request.Username))
			if len(request.Username) < 3 || len(request.Username) > 64 {
				_ = conn.WriteJSON(map[string]any{"type": "error", "request_id": request.RequestID, "code": "invalid_username", "message": "username must contain 3 to 64 characters"})
				continue
			}
			available, err := a.users.UsernameAvailable(context.Background(), request.Username)
			if err != nil {
				_ = conn.WriteJSON(map[string]any{"type": "error", "request_id": request.RequestID, "code": "availability_unavailable", "message": "could not verify username"})
				continue
			}
			if err := conn.WriteJSON(map[string]any{"type": "username_availability", "request_id": request.RequestID, "username": request.Username, "available": available}); err != nil {
				return
			}
		}
		_ = conn.WriteJSON(map[string]any{"type": "error", "code": "rate_limited", "message": fmt.Sprintf("maximum of %d checks per connection", 60)})
	}); err != nil {
		return
	}
}
