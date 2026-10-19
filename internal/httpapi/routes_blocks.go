package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/jackc/pgx/v5"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/openim"
)

type blockInput struct {
	PingID string `json:"ping_id" validate:"required,len=12,numeric"`
}

type blockResult struct {
	UserID    string    `json:"user_id"`
	PingID    string    `json:"ping_id"`
	Nickname  string    `json:"nickname"`
	Image     *string   `json:"image"`
	BlockedAt time.Time `json:"blocked_at"`
}

func (a *API) registerBlockRoutes(r *router.Router) {
	r.GET("/v1/blocks", a.listBlocks)
	r.POST("/v1/blocks", a.addBlock)
	r.DELETE("/v1/blocks/{pingID}", a.removeBlock)
}

func (a *API) listBlocks(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "blocks-list", userID, friendsListUserLimit, friendsListIPLimit) {
		return
	}
	openIMID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	blocks, err := a.openIM.ListBlocks(context.Background(), &openim.ListBlocksRequest{UserID: openIMID})
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not load blocked users"})
		return
	}
	ids := make([]string, 0, len(blocks))
	for _, b := range blocks {
		ids = append(ids, b.UserID)
	}
	profiles, err := a.users.PublicProfilesByOpenIMIDs(context.Background(), ids)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load blocked profiles"})
		return
	}
	byID := make(map[string]*auth.PublicProfile, len(profiles))
	for _, p := range profiles {
		byID[p.OpenIMUserID] = p
	}
	result := make([]blockResult, 0, len(blocks))
	for _, b := range blocks {
		p := byID[b.UserID]
		if p == nil {
			continue
		}
		nickname := p.Nickname
		if nickname == "" {
			nickname = p.Username
		}
		blockedAt := time.UnixMilli(b.CreatedAt)
		if b.CreatedAt <= 0 {
			blockedAt = time.Time{}
		}
		result = append(result, blockResult{UserID: p.UserID, PingID: p.PingID, Nickname: nickname, Image: p.Image, BlockedAt: blockedAt})
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"blocks": result})
}

func (a *API) addBlock(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "blocks-write", userID, blockUserLimit, blockIPLimit) {
		return
	}
	var req blockInput
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	target, err := a.users.UserByPingID(context.Background(), strings.TrimSpace(req.PingID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load user"})
		return
	}
	selfOpenIM, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil || target.OpenIMUserID == "" || target.OpenIMUserID == selfOpenIM {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "cannot block this user"})
		return
	}
	if err = a.openIM.AddBlock(context.Background(), &openim.BlockRequest{OwnerUserID: selfOpenIM, BlockUserID: target.OpenIMUserID}); err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not block user"})
		return
	}
	writeJSON(ctx, http.StatusCreated, map[string]any{"ping_id": target.PingID, "blocked_at": time.Now().UTC()})
}

func (a *API) removeBlock(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "blocks-write", userID, blockUserLimit, blockIPLimit) {
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
	selfOpenIM, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	if err = a.openIM.RemoveBlock(context.Background(), &openim.BlockRequest{OwnerUserID: selfOpenIM, BlockUserID: target.OpenIMUserID}); err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not unblock user"})
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}
