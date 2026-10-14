package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/disappearing"
	"pingmessenger/internal/timers"
)

type disappearingUpdateRequest struct {
	DurationSeconds *int  `json:"duration_seconds"`
	Enabled         *bool `json:"enabled"`
}

func (a *API) registerDisappearingRoutes(r *router.Router) {
	r.GET("/v1/settings/timers", a.listGlobalTimers)
	r.GET("/v1/settings/timers/{timerKey}", a.getGlobalTimer)
	r.GET("/v1/conversations/{conversationID}/disappearing", a.getDisappearingSetting)
	r.PUT("/v1/conversations/{conversationID}/disappearing", a.setDisappearingSetting)
	r.DELETE("/v1/conversations/{conversationID}/disappearing", a.deleteDisappearingSetting)
	r.GET("/v1/conversations/{conversationID}/disappearing/ws", a.disappearingWebSocket)
}

func (a *API) listGlobalTimers(ctx *fasthttp.RequestCtx) {
	if _, _, ok := a.authenticated(ctx); !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	items, err := a.timers.List(context.Background())
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not list timers"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"timers": items})
}

func (a *API) getGlobalTimer(ctx *fasthttp.RequestCtx) {
	if _, _, ok := a.authenticated(ctx); !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	key := strings.TrimSpace(ctx.UserValue("timerKey").(string))
	timer, err := a.timers.Get(context.Background(), key)
	if err == timers.ErrNotFound {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "timer not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load timer"})
		return
	}
	writeJSON(ctx, http.StatusOK, timer)
}

func (a *API) getDisappearingSetting(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	conversation := conversationID(ctx)
	setting, err := a.disappearing.Get(context.Background(), userID, conversation)
	if err == disappearing.ErrNotFound {
		setting = &disappearing.Setting{ConversationID: conversation, UserID: userID, DurationSeconds: 86400, Enabled: true}
		if timer, timerErr := a.timers.Get(context.Background(), "disappearing_message_default"); timerErr == nil {
			setting.DurationSeconds, setting.Enabled = timer.DurationSeconds, timer.Enabled
		}
		writeJSON(ctx, http.StatusOK, setting)
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load disappearing setting"})
		return
	}
	writeJSON(ctx, http.StatusOK, setting)
}

func (a *API) setDisappearingSetting(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req disappearingUpdateRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	duration := 86400
	if timer, err := a.timers.Get(context.Background(), "disappearing_message_default"); err == nil {
		duration = timer.DurationSeconds
	}
	if req.DurationSeconds != nil {
		duration = *req.DurationSeconds
	}
	if duration < 0 || duration > 2592000 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "duration_seconds must be between 0 and 2592000"})
		return
	}
	enabled := duration > 0
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if !enabled {
		duration = 0
	}
	setting, err := a.disappearing.Set(context.Background(), userID, conversationID(ctx), duration, enabled)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not save disappearing setting"})
		return
	}
	writeJSON(ctx, http.StatusOK, setting)
}

func (a *API) deleteDisappearingSetting(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := a.disappearing.Disable(context.Background(), userID, conversationID(ctx)); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not disable disappearing messages"})
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

func (a *API) disappearingWebSocket(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	conversation := conversationID(ctx)
	upgrader := websocket.FastHTTPUpgrader{HandshakeTimeout: 10 * time.Second, CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
		origin := strings.TrimSpace(string(ctx.Request.Header.Peek("Origin")))
		return origin == "" || strings.Contains(origin, string(ctx.Host()))
	}}
	if err := upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
		defer conn.Close()
		conn.SetReadLimit(4096)
		events, unsubscribe := a.disappearing.Subscribe(conversation)
		defer unsubscribe()
		if setting, err := a.disappearing.Get(context.Background(), userID, conversation); err == nil {
			_ = conn.WriteJSON(disappearing.Event{Type: "disappearing.updated", ConversationID: conversation, UserID: userID, DurationSeconds: setting.DurationSeconds, Enabled: setting.Enabled, UpdatedAt: setting.UpdatedAt})
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
			case event, open := <-events:
				if !open {
					return
				}
				if err := conn.WriteJSON(event); err != nil {
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
