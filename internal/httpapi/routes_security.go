package httpapi

import (
	"context"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"net/http"
	"pingmessenger/internal/auth"
	"strings"
	"time"
)

func (a *API) registerSecurityRoutes(r *router.Router) {
	r.GET("/v1/security/mfa", a.listMFA)
	r.POST("/v1/security/mfa/factors", a.enrollMFA)
	r.POST("/v1/security/mfa/factors/verify", a.verifyMFAEnrollment)
	r.POST("/v1/security/mfa/factors/disable", a.disableMFA)
	r.GET("/v1/security/backup-codes", a.backupCodes)
	r.POST("/v1/security/backup-codes/regenerate", a.regenerateBackupCodes)
	r.GET("/v1/security/activity", a.activity)
	r.POST("/v1/security/revoke", a.revoke)
}
func (a *API) listMFA(ctx *fasthttp.RequestCtx) {
	uid, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, 401, map[string]string{"error": "unauthorized"})
		return
	}
	f, err := a.users.MFAFactors(context.Background(), uid)
	if err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not list MFA factors"})
		return
	}
	writeJSON(ctx, 200, map[string]any{"factors": f})
}

type enrollMFARequest struct {
	Kind      string `json:"kind" validate:"required,oneof=email phone totp"`
	ContactID string `json:"contact_id" validate:"omitempty,uuid4"`
	Contact   string `json:"contact" validate:"omitempty,max=254"`
	Label     string `json:"label" validate:"omitempty,max=128"`
}

func (a *API) enrollMFA(ctx *fasthttp.RequestCtx) {
	uid, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var req enrollMFARequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	if req.Kind != "totp" {
		if req.ContactID == "" && strings.TrimSpace(req.Contact) == "" {
			writeJSON(ctx, 422, map[string]string{"error": "contact_id or contact is required"})
			return
		}
		var contact *auth.Contact
		var err error
		if req.ContactID != "" {
			contact, err = a.users.ContactForMFA(context.Background(), uid, req.Kind, req.ContactID)
		} else {
			contact, err = a.users.ContactForMFAValue(context.Background(), uid, req.Kind, strings.TrimSpace(req.Contact))
		}
		if err != nil || !contact.Verified {
			writeJSON(ctx, 422, map[string]string{"error": "contact must be verified before MFA enrollment"})
			return
		}
		req.ContactID = contact.ID
		id, err := a.users.BeginMFAFactor(context.Background(), uid, req.Kind, req.ContactID, "", req.Label)
		if err != nil {
			writeJSON(ctx, 422, map[string]string{"error": "could not enroll MFA factor"})
			return
		}
		if err = a.users.ActivateMFAFactor(context.Background(), uid, id); err != nil {
			writeJSON(ctx, 500, map[string]string{"error": "could not activate MFA factor"})
			return
		}
		writeJSON(ctx, 201, map[string]string{"factor_id": id, "kind": req.Kind, "status": "enabled"})
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not create TOTP secret"})
		return
	}
	encrypted, err := a.deviceTokenCipher.Encrypt(secret)
	if err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not protect TOTP secret"})
		return
	}
	id, err := a.users.BeginMFAFactor(context.Background(), uid, "totp", "", encrypted, req.Label)
	if err != nil {
		writeJSON(ctx, 422, map[string]string{"error": "could not enroll MFA factor"})
		return
	}
	writeJSON(ctx, 201, map[string]string{"factor_id": id, "kind": "totp", "secret": secret, "status": "setup_required"})
}

type verifyMFAEnrollmentRequest struct {
	FactorID string `json:"factor_id" validate:"required,uuid4"`
	Code     string `json:"code" validate:"required,len=6,numeric"`
}

func (a *API) verifyMFAEnrollment(ctx *fasthttp.RequestCtx) {
	uid, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var req verifyMFAEnrollmentRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	f, err := a.users.MFAFactorEnrollment(context.Background(), uid, req.FactorID)
	if err != nil || f.Kind != "totp" {
		writeJSON(ctx, 422, map[string]string{"error": "TOTP factor not found"})
		return
	}
	secret, err := a.deviceTokenCipher.Decrypt(f.SecretCiphertext)
	if err != nil || !auth.TOTPMatches(secret, req.Code, time.Now()) {
		writeJSON(ctx, 401, map[string]string{"error": "invalid TOTP code"})
		return
	}
	if err = a.users.ActivateMFAFactor(ctx, uid, req.FactorID); err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not activate TOTP factor"})
		return
	}
	writeJSON(ctx, 200, map[string]string{"factor_id": req.FactorID, "status": "enabled"})
}

type disableMFARequest struct {
	FactorID    string `json:"factor_id" validate:"required,uuid4"`
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
}

func (a *API) disableMFA(ctx *fasthttp.RequestCtx) {
	uid, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var req disableMFARequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	challenge, err := a.users.GetChallenge(context.Background(), req.ChallengeID)
	if err != nil || challenge.UserID != uid || challenge.Purpose != "step_up" || challenge.Consumed || challenge.Attempts >= 5 || time.Now().After(challenge.ExpiresAt) || !auth.OTPMatches(a.otpSecret, req.Code, challenge.CodeHash) {
		if challenge != nil {
			_ = a.users.RecordChallengeFailure(context.Background(), challenge.ID)
		}
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "valid step-up verification required"})
		return
	}
	if err = a.users.ConsumeChallenge(context.Background(), challenge); err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "valid step-up verification required"})
		return
	}
	if err := a.users.DisableMFAFactor(context.Background(), uid, req.FactorID); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": "could not disable MFA factor"})
		return
	}
	writeJSON(ctx, 200, map[string]string{"status": "disabled"})
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
