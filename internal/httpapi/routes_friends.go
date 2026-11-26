package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/fasthttp/router"
	"github.com/jackc/pgx/v5"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/openim"
)

func (a *API) registerFriendRoutes(r *router.Router) {
	r.POST("/v1/friends/discover-network", a.discoverNetwork)
	r.POST("/v1/friends/search", a.searchFriends)
	r.GET("/v1/friends", a.listFriends)
	r.GET("/v1/contacts/discovered", a.listDiscoveredContacts)
	r.POST("/v1/contacts/discovered", a.addDiscoveredContacts)
	r.DELETE("/v1/contacts/discovered", a.clearDiscoveredContacts)
	r.GET("/v1/users/{pingID}/status", a.userStatus)
}

const (
	userStatusAvailable = "AVAILABLE"
	userStatusBlockedBy = "BLOCKED_BY_USER"
)

type friendProfileResult struct {
	UserID       string  `json:"user_id"`
	OpenIMUserID string  `json:"openim_user_id"`
	PingID       string  `json:"ping_id"`
	Username     string  `json:"username,omitempty"`
	Nickname     string  `json:"nickname"`
	Image        *string `json:"image"`
	StatusCode   string  `json:"status_code,omitempty"`
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
	blocked, err := a.blockedByRequester(context.Background(), openIMID, friendIDs)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not check friend visibility"})
		return
	}
	friends := make([]friendProfileResult, 0, len(profiles))
	for _, p := range profiles {
		nickname := p.Nickname
		if nickname == "" {
			nickname = p.Username
		}
		statusCode := userStatusAvailable
		image := p.Image
		if blocked[p.OpenIMUserID] {
			statusCode, image = userStatusBlockedBy, nil
		}
		friends = append(friends, friendProfileResult{UserID: p.UserID, OpenIMUserID: p.OpenIMUserID, PingID: p.PingID, Username: p.Username, Nickname: nickname, Image: image, StatusCode: statusCode})
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
	StatusCode   string  `json:"status_code,omitempty"`
}

type discoverRequest struct {
	Emails   []string `json:"emails" validate:"max=100,dive,email"`
	MobileNo []string `json:"mobile_no" validate:"max=100,dive,max=32"`
}

type discoveredContactsRequest struct {
	OpenIMUserIDs []string `json:"openim_user_ids" validate:"required,min=1,max=1000,dive,required,max=128"`
}

func (a *API) listDiscoveredContacts(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ids, err := a.users.DiscoveredOpenIMIDs(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load discovered contacts"})
		return
	}
	profiles, err := a.users.PublicProfilesByOpenIMIDs(context.Background(), ids)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load discovered contact profiles"})
		return
	}
	openIMID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	blocked, err := a.blockedByRequester(context.Background(), openIMID, ids)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not check contact visibility"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"contacts": friendProfilesWithStatus(profiles, blocked)})
}

func (a *API) addDiscoveredContacts(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req discoveredContactsRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	openIMIDs := make([]string, 0, len(req.OpenIMUserIDs))
	seen := make(map[string]struct{}, len(req.OpenIMUserIDs))
	for _, id := range req.OpenIMUserIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "openim_user_ids must not contain empty IDs"})
			return
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			openIMIDs = append(openIMIDs, id)
		}
	}
	platformIDs, err := a.users.PlatformUserIDsByOpenIM(context.Background(), openIMIDs)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not resolve discovered contacts"})
		return
	}
	contactIDs := make([]string, 0, len(platformIDs))
	for _, id := range platformIDs {
		if id != userID {
			contactIDs = append(contactIDs, id)
		}
	}
	if err := a.users.AddDiscoveredContacts(context.Background(), userID, contactIDs); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not save discovered contacts"})
		return
	}
	ids, err := a.users.DiscoveredOpenIMIDs(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load discovered contacts"})
		return
	}
	profiles, err := a.users.PublicProfilesByOpenIMIDs(context.Background(), ids)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load discovered contact profiles"})
		return
	}
	openIMID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	blocked, err := a.blockedByRequester(context.Background(), openIMID, ids)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not check contact visibility"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"contacts": friendProfilesWithStatus(profiles, blocked)})
}

func (a *API) clearDiscoveredContacts(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := a.users.ClearDiscoveredContacts(context.Background(), userID); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not clear discovered contacts"})
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

func friendProfiles(profiles []*auth.PublicProfile) []friendProfileResult {
	result := make([]friendProfileResult, 0, len(profiles))
	for _, p := range profiles {
		nickname := p.Nickname
		if nickname == "" {
			nickname = p.Username
		}
		result = append(result, friendProfileResult{UserID: p.UserID, OpenIMUserID: p.OpenIMUserID, PingID: p.PingID, Username: p.Username, Nickname: nickname, Image: p.Image})
	}
	return result
}

func friendProfilesWithStatus(profiles []*auth.PublicProfile, blocked map[string]bool) []friendProfileResult {
	results := friendProfiles(profiles)
	for i := range results {
		results[i].StatusCode = userStatusAvailable
		if blocked[results[i].OpenIMUserID] {
			results[i].Image = nil
			results[i].StatusCode = userStatusBlockedBy
		}
	}
	return results
}

// blockedByRequester checks each candidate directionally. Only the target's
// block of the requester is relevant to profile/story visibility.
func (a *API) blockedByRequester(ctx context.Context, requesterOpenIMID string, targetOpenIMIDs []string) (map[string]bool, error) {
	blocked := make(map[string]bool)
	if batch, ok := a.openIM.(openim.BlockStatusBatchChecker); ok && requesterOpenIMID != "" {
		for start := 0; start < len(targetOpenIMIDs); start += 1000 {
			end := start + 1000
			if end > len(targetOpenIMIDs) {
				end = len(targetOpenIMIDs)
			}
			chunk, err := batch.BlockedBy(ctx, requesterOpenIMID, targetOpenIMIDs[start:end])
			if err != nil {
				return nil, err
			}
			for userID, isBlocked := range chunk {
				if isBlocked {
					blocked[userID] = true
				}
			}
		}
		return blocked, nil
	}
	checker, ok := a.openIM.(openim.BlockStatusChecker)
	if !ok || requesterOpenIMID == "" {
		return nil, errors.New("OpenIM directional block status is unavailable")
	}
	for _, targetID := range targetOpenIMIDs {
		if targetID == "" || targetID == requesterOpenIMID {
			continue
		}
		isBlocked, err := checker.IsBlocked(ctx, targetID, requesterOpenIMID)
		if err != nil {
			return nil, err
		}
		if isBlocked {
			blocked[targetID] = true
		}
	}
	return blocked, nil
}

func (a *API) userStatus(ctx *fasthttp.RequestCtx) {
	requesterID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "user-status", requesterID, friendsListUserLimit, friendsListIPLimit) {
		return
	}
	pingID := strings.TrimSpace(ctx.UserValue("pingID").(string))
	if len(pingID) != 12 || !isDigits(pingID) {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "ping_id must be 12 digits"})
		return
	}
	target, err := a.users.UserByPingID(context.Background(), pingID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load user"})
		return
	}
	requesterOpenIMID, err := a.users.OpenIMUserID(context.Background(), requesterID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	blocked, err := a.blockedByRequester(context.Background(), requesterOpenIMID, []string{target.OpenIMUserID})
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not check user status"})
		return
	}
	if blocked[target.OpenIMUserID] {
		ctx.Response.Header.Set("X-PingMessenger-User-Status", userStatusBlockedBy)
		writeJSON(ctx, http.StatusForbidden, map[string]string{"code": userStatusBlockedBy, "error": "user blocked you"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"status_code": userStatusAvailable})
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
	requesterOpenIMID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	blocked, err := a.blockedByRequester(context.Background(), requesterOpenIMID, ids)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not check discovered contact visibility"})
		return
	}
	visible := make([]string, 0, len(ids))
	for _, id := range ids {
		if !blocked[id] {
			visible = append(visible, id)
		}
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"open_im_user_ids": visible})
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
	requesterOpenIMID, err := a.users.OpenIMUserID(context.Background(), requesterID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	userIDs := make([]string, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.OpenIMUserID)
	}
	blocked, err := a.blockedByRequester(context.Background(), requesterOpenIMID, userIDs)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not check search result visibility"})
		return
	}
	results := make([]friendSearchResult, 0, len(users))
	for _, user := range users {
		isBlockedByUser := blocked[user.OpenIMUserID]
		if req.Field == "username" && isBlockedByUser {
			continue
		}
		nickname := strings.TrimSpace(user.Nickname)
		if nickname == "" {
			nickname = user.Username
		}
		if nickname == "" {
			nickname = user.PingID
		}
		statusCode := userStatusAvailable
		if isBlockedByUser {
			statusCode = userStatusBlockedBy
		}
		results = append(results, friendSearchResult{
			UserID: user.OpenIMUserID, Nickname: nickname, FaceURL: visibleImage(user.Image, isBlockedByUser),
			Remark: "", Relationship: -1, Username: user.Username, PingID: user.PingID,
			StatusCode: statusCode,
		})
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"results": results, "total": len(results)})
}

func visibleImage(image *string, blockedByUser bool) *string {
	if blockedByUser {
		return nil
	}
	return image
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
