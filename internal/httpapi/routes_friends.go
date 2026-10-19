package httpapi

import (
	"context"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"net/http"
	"net/mail"
	"pingmessenger/internal/openim"
	"strings"
)

func (a *API) registerFriendRoutes(r *router.Router) {
	r.POST("/v1/friends/discover-network", a.discoverNetwork)
	r.POST("/v1/friends/search", a.searchFriends)
	r.GET("/v1/friends", a.listFriends)
}

type friendProfileResult struct {
	UserID       string  `json:"user_id"`
	OpenIMUserID string  `json:"openim_user_id"`
	PingID       string  `json:"ping_id"`
	Username     string  `json:"username,omitempty"`
	Nickname     string  `json:"nickname"`
	Image        *string `json:"image"`
}

func (a *API) listFriends(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "friends-list", userID, friendsListUserLimit, friendsListIPLimit) {
		return
	}
	openIMID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	friendIDs, err := a.openIM.ListFriendIDs(context.Background(), &openim.ListFriendIDsRequest{UserID: openIMID})
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not load friends"})
		return
	}
	profiles, err := a.users.PublicProfilesByOpenIMIDs(context.Background(), friendIDs)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load friend profiles"})
		return
	}
	friends := make([]friendProfileResult, 0, len(profiles))
	for _, p := range profiles {
		nickname := p.Nickname
		if nickname == "" {
			nickname = p.Username
		}
		friends = append(friends, friendProfileResult{UserID: p.UserID, OpenIMUserID: p.OpenIMUserID, PingID: p.PingID, Username: p.Username, Nickname: nickname, Image: p.Image})
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"friends": friends})
}

type friendSearchRequest struct {
	Query string `json:"query" validate:"required,min=1,max=254"`
	Field string `json:"field" validate:"required,oneof=username email ping_id phone"`
	Limit int    `json:"limit,omitempty" validate:"omitempty,min=1,max=50"`
}

type friendSearchResult struct {
	UserID       string  `json:"userID"`
	Nickname     string  `json:"nickname"`
	FaceURL      *string `json:"faceURL"`
	Remark       string  `json:"remark"`
	Relationship int     `json:"relationship"`
	Username     string  `json:"username"`
	PingID       string  `json:"ping_id"`
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

func (a *API) searchFriends(ctx *fasthttp.RequestCtx) {
	requesterID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req friendSearchRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	req.Field = strings.ToLower(strings.TrimSpace(req.Field))
	req.Query = strings.TrimSpace(req.Query)
	if req.Limit == 0 {
		req.Limit = 20
	}
	switch req.Field {
	case "username":
		req.Query = strings.ToLower(req.Query)
	case "email":
		req.Query = strings.ToLower(req.Query)
		parsed, err := mail.ParseAddress(req.Query)
		if err != nil || !strings.EqualFold(parsed.Address, req.Query) {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "query must be a valid email address"})
			return
		}
	case "phone":
		if len(req.Query) > 32 {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "phone query is too long"})
			return
		}
	case "ping_id":
		if len(req.Query) != 12 || !isDigits(req.Query) {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "ping_id query must be 12 digits"})
			return
		}
	}
	users, err := a.users.SearchUsers(context.Background(), requesterID, req.Field, req.Query, req.Limit)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not search users"})
		return
	}
	results := make([]friendSearchResult, 0, len(users))
	for _, user := range users {
		nickname := strings.TrimSpace(user.Nickname)
		if nickname == "" {
			nickname = user.Username
		}
		if nickname == "" {
			nickname = user.PingID
		}
		results = append(results, friendSearchResult{
			UserID: user.OpenIMUserID, Nickname: nickname, FaceURL: user.Image,
			Remark: "", Relationship: -1, Username: user.Username, PingID: user.PingID,
		})
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"results": results, "total": len(results)})
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
