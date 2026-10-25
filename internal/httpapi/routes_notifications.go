package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/notifications"
)

type notificationResolveRequest struct {
	OpenIMUserIDs  []string `json:"openim_user_ids"`
	ConversationID string   `json:"conversation_id"`
}

type notificationResolveResponse struct {
	Preferences map[string]notifications.Settings `json:"preferences"`
}

func (a *API) registerNotificationRoutes(r *router.Router) {
	r.GET("/v1/settings/notifications", a.getNotificationPreferences)
	r.PUT("/v1/settings/notifications", a.putNotificationPreferences)
	r.GET("/v1/conversations/{conversationID}/notifications", a.getConversationNotificationPreferences)
	r.PUT("/v1/conversations/{conversationID}/notifications", a.putConversationNotificationPreferences)
	r.DELETE("/v1/conversations/{conversationID}/notifications", a.deleteConversationNotificationPreferences)
	r.POST("/v1/internal/notifications/resolve", a.resolveNotificationPreferences)
}

func (a *API) getNotificationPreferences(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	settings, err := a.notificationPreferences.GetGlobal(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load notification preferences"})
		return
	}
	writeJSON(ctx, http.StatusOK, settings)
}

func (a *API) putNotificationPreferences(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input notifications.Input
	if err := a.DecodeAndValidate(ctx, &input); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := validateNotificationInput(input); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	settings, err := a.notificationPreferences.PutGlobal(context.Background(), userID, input)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not save notification preferences"})
		return
	}
	writeJSON(ctx, http.StatusOK, settings)
}

func (a *API) getConversationNotificationPreferences(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	conversationID := strings.TrimSpace(ctx.UserValue("conversationID").(string))
	if !validConversationID(conversationID) {
		writeJSON(ctx, http.StatusBadRequest, map[string]string{"error": "invalid conversation_id"})
		return
	}
	settings, err := a.notificationPreferences.GetConversation(context.Background(), userID, conversationID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load notification preferences"})
		return
	}
	writeJSON(ctx, http.StatusOK, settings)
}

func (a *API) putConversationNotificationPreferences(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	conversationID := strings.TrimSpace(ctx.UserValue("conversationID").(string))
	if !validConversationID(conversationID) {
		writeJSON(ctx, http.StatusBadRequest, map[string]string{"error": "invalid conversation_id"})
		return
	}
	var input notifications.Input
	if err := a.DecodeAndValidate(ctx, &input); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := validateNotificationInput(input); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	settings, err := a.notificationPreferences.PutConversation(context.Background(), userID, conversationID, input)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not save notification preferences"})
		return
	}
	writeJSON(ctx, http.StatusOK, settings)
}

func (a *API) deleteConversationNotificationPreferences(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	conversationID := strings.TrimSpace(ctx.UserValue("conversationID").(string))
	if !validConversationID(conversationID) {
		writeJSON(ctx, http.StatusBadRequest, map[string]string{"error": "invalid conversation_id"})
		return
	}
	if err := a.notificationPreferences.DeleteConversation(context.Background(), userID, conversationID); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not remove notification preferences"})
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

// resolveNotificationPreferences is private to OpenIM and returns only active,
// known Platform accounts. Unknown IDs are omitted so callers can fail safely.
func (a *API) resolveNotificationPreferences(ctx *fasthttp.RequestCtx) {
	if a.openIMServiceToken == "" {
		writeJSON(ctx, http.StatusServiceUnavailable, map[string]string{"error": "notification resolver is not configured"})
		return
	}
	if !a.validOpenIMServiceToken(strings.TrimSpace(string(ctx.Request.Header.Peek("X-OpenIM-Service-Token")))) {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request notificationResolveRequest
	if err := a.DecodeAndValidate(ctx, &request); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if len(request.OpenIMUserIDs) == 0 || len(request.OpenIMUserIDs) > 1000 || !validConversationID(request.ConversationID) {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "openim_user_ids must contain 1..1000 IDs and conversation_id must be valid"})
		return
	}
	ids := make([]string, 0, len(request.OpenIMUserIDs))
	seen := make(map[string]struct{}, len(request.OpenIMUserIDs))
	for _, id := range request.OpenIMUserIDs {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 128 {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid openim_user_id"})
			return
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	preferences, err := a.notificationPreferences.Resolve(context.Background(), ids, request.ConversationID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not resolve notification preferences"})
		return
	}
	writeJSON(ctx, http.StatusOK, notificationResolveResponse{Preferences: preferences})
}

func validConversationID(id string) bool { return len(id) > 0 && len(id) <= 256 }

func validateNotificationInput(input notifications.Input) error {
	if input.Enabled == nil && input.Mode == nil && input.Sound == nil && input.MaxUpdateFrequencySeconds == nil {
		return errors.New("at least one notification preference is required")
	}
	if input.Mode != nil && *input.Mode != "all" && *input.Mode != "mentions_only" && *input.Mode != "none" {
		return errors.New("mode must be all, mentions_only, or none")
	}
	if input.Sound != nil && *input.Sound != "default" && *input.Sound != "silent" {
		return errors.New("sound must be default or silent")
	}
	if input.MaxUpdateFrequencySeconds != nil && *input.MaxUpdateFrequencySeconds != 2 && *input.MaxUpdateFrequencySeconds != 5 && *input.MaxUpdateFrequencySeconds != 10 {
		return errors.New("max_update_frequency_seconds must be 2, 5, or 10")
	}
	return nil
}
