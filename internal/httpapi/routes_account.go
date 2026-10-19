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
	openIMUserID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err == nil && openIMUserID != "" {
		if err = a.openIM.DeleteUser(context.Background(), &openim.DeleteUserRequest{UserID: openIMUserID}); err != nil {
			// Do not destroy the platform account while the messaging identity is
			// still live; retrying the same request is safe and idempotent.
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not remove OpenIM account"})
			return
		}
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
	for _, object := range deletion.Objects {
		store := a.objects
		if object.Kind == "document" {
			store = a.documentObjects
		}
		if store != nil {
			if deleteErr := store.Delete(context.Background(), object.Key); deleteErr != nil {
				log.Printf("account %s object cleanup failed (%s): %v", userID, object.Key, deleteErr)
			}
		}
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"status": "deleted"})
}
