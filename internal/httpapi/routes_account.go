package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/openim"
)

type accountDeleteRequest struct {
	CurrentPassword string `json:"current_password" validate:"required,max=256"`
	AcceptTerms     bool   `json:"accept_terms" validate:"required"`
}

func (a *API) registerAccountRoutes(r *router.Router) {
	r.POST("/v1/account/delete", a.deleteAccount)
}

func (a *API) deleteAccount(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "account-delete", userID, accountDeleteUserLimit, accountDeleteIPLimit) {
		return
	}
	var req accountDeleteRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	account, err := a.users.PasswordLoginByID(context.Background(), userID)
	if err != nil || !auth.VerifyPassword(req.CurrentPassword, account.PasswordHash) {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid current password"})
		return
	}
	deletion, err := a.users.DeleteAccount(context.Background(), userID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeJSON(ctx, http.StatusRequestTimeout, map[string]string{"error": "account deletion timed out"})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not delete account"})
		return
	}
	// Account deletion is deliberately a platform soft-delete. Do not invoke
	// OpenIM user/delete_users and do not remove S3/application rows: retained
	// messages and payment history remain addressable for audit/retention. The
	// platform transaction has already revoked every local session, so a failed
	// OpenIM notification cannot reopen the account for platform login.
	if deletion.OpenIMUserID != "" && !deletion.AlreadyDeleted {
		for platformID := int32(1); platformID <= 12; platformID++ {
			if revokeErr := a.openIM.ForceLogout(context.Background(), &openim.ForceLogoutRequest{UserID: deletion.OpenIMUserID, PlatformID: platformID}); revokeErr != nil {
				log.Printf("account %s OpenIM force logout platform %d failed after soft-delete: %v", userID, platformID, revokeErr)
			}
		}
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"status": "deleted", "deleted_at": deletion.DeletedAt})
}
