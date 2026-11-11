package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/valyala/fasthttp"
)

const defaultTermsRequiredVersion = "2026-11-10"

func optionalTermsVersion(version string) *string {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	return &version
}

func (a *API) ConfigureTermsRequiredVersion(version string) {
	version = strings.TrimSpace(version)
	if version != "" {
		a.termsRequiredVersion = version
	}
}

func (a *API) acceptTerms(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		TermsVersion string `json:"terms_version" validate:"required,max=64"`
	}
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	req.TermsVersion = strings.TrimSpace(req.TermsVersion)
	if req.TermsVersion == "" {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "terms_version is required"})
		return
	}
	if req.TermsVersion != a.termsRequiredVersion {
		writeJSON(ctx, http.StatusConflict, map[string]string{"error": "terms_version_not_current"})
		return
	}
	if err := a.users.AcceptTerms(context.Background(), userID, req.TermsVersion); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not record terms acceptance"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"terms_accepted_version": req.TermsVersion, "terms_required_version": a.termsRequiredVersion})
}
