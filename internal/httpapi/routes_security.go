package httpapi

import (
	"context"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"net/http"
	"pingmessenger/internal/auth"
	"time"
)

func (a *API) registerSecurityRoutes(r *router.Router) {
	r.GET("/v1/security/backup-codes", a.backupCodes)
	r.POST("/v1/security/backup-codes/regenerate", a.regenerateBackupCodes)
	r.GET("/v1/security/activity", a.activity)
	r.POST("/v1/security/revoke", a.revoke)
}
func (a *API) backupCodes(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	n, err := a.users.RecoveryCodeCount(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not retrieve recovery-code status"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]int{"remaining": n})
}

type regenerateCodesRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
}

func (a *API) regenerateBackupCodes(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req regenerateCodesRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	ch, err := a.users.GetChallenge(context.Background(), req.ChallengeID)
	if err != nil || ch.UserID != userID || ch.Purpose != "step_up" || ch.Consumed || ch.Attempts >= 5 || time.Now().After(ch.ExpiresAt) || !auth.OTPMatches(a.otpSecret, req.Code, ch.CodeHash) {
		if ch != nil {
			_ = a.users.RecordChallengeFailure(context.Background(), ch.ID)
		}
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "valid step-up verification required"})
		return
	}
	if err = a.users.ConsumeChallenge(context.Background(), ch); err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "valid step-up verification required"})
		return
	}
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		raw, _, err := a.tokens.NewRefresh()
		if err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not generate recovery codes"})
			return
		}
		codes[i] = raw
		hashes[i] = a.tokens.Hash(raw)
	}
	if err = a.users.ReplaceRecoveryCodes(context.Background(), userID, hashes); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not store recovery codes"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"recovery_codes": codes})
}
func (a *API) activity(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sessions, err := a.users.ListActivity(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not list activity"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"sessions": sessions})
}

type revokeRequest struct {
	SessionIDs []string `json:"session_ids" validate:"omitempty,dive,uuid4"`
	All        bool     `json:"all"`
}

func (a *API) revoke(ctx *fasthttp.RequestCtx) {
	userID, currentID, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req revokeRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := a.users.RevokeSessions(context.Background(), userID, currentID, req.SessionIDs, req.All); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(ctx, http.StatusNoContent, nil)
}
