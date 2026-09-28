package httpapi

import (
	"context"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"net/http"
	"strings"
)

func (a *API) registerFriendRoutes(r *router.Router) {
	r.POST("/v1/friends/discover-network", a.discoverNetwork)
}

type discoverRequest struct {
	Emails   []string `json:"emails" validate:"max=100,dive,email"`
	MobileNo []string `json:"mobile_no" validate:"max=100,dive,max=32"`
}

func (a *API) discoverNetwork(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req discoverRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	emails := make([]string, 0, len(req.Emails))
	for _, v := range req.Emails {
		emails = append(emails, strings.ToLower(strings.TrimSpace(v)))
	}
	phones := make([]string, 0, len(req.MobileNo))
	for _, v := range req.MobileNo {
		phones = append(phones, strings.TrimSpace(v))
	}
	if len(emails)+len(phones) == 0 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "at least one email or mobile number is required"})
		return
	}
	ids, err := a.users.DiscoverOpenIMUsers(context.Background(), userID, emails, phones)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not discover contacts"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"open_im_user_ids": ids})
}
