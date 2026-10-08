package httpapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/fasthttp/router"
	"github.com/jackc/pgx/v5"
	"github.com/valyala/fasthttp"
	"net/http"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/notify"
	"strings"
	"time"
)

func (a *API) registerAuthRoutes(r *router.Router) {
	r.POST("/v1/auth/signup", a.signup)
	r.POST("/v1/auth/verify-username", a.verifyUsername)
	r.POST("/v1/auth/login", a.loginPassword)
	r.POST("/v1/auth/login/start", a.loginStart)
	r.POST("/v1/auth/login/verify", a.loginVerify)
	r.POST("/v1/auth/login/mfa/verify", a.loginMFAVerify)
	r.POST("/v1/auth/refresh", a.refresh)
}

type passwordLoginRequest struct {
	Username   string `json:"username" validate:"omitempty,min=3,max=64"`
	Email      string `json:"email" validate:"omitempty,email,max=254"`
	Password   string `json:"password" validate:"required,min=12,max=256"`
	PlatformID string `json:"platform_id" validate:"required,max=64"`
	DeviceName string `json:"device_name" validate:"omitempty,max=128"`
	DeviceID   string `json:"device_id" validate:"omitempty,max=128"`
}

// loginPassword is the primary login route. It deliberately has no contact or
// OTP dependency: email is optional account metadata, not an auth prerequisite.
func (a *API) loginPassword(ctx *fasthttp.RequestCtx) {
	var req passwordLoginRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	platform, ok := auth.NormalizePlatform(req.PlatformID)
	if !ok {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported platform_id"})
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if (username == "") == (email == "") {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "provide exactly one of username or email"})
		return
	}
	var account *auth.PasswordLogin
	var err error
	if username != "" {
		account, err = a.users.PasswordLogin(context.Background(), username)
	} else {
		account, err = a.users.PasswordLoginByEmail(context.Background(), email)
	}
	if err != nil || !auth.VerifyPassword(req.Password, account.PasswordHash) {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	factor, factorErr := a.users.PreferredMFAFactor(context.Background(), account.UserID)
	if factorErr != nil && !errors.Is(factorErr, pgx.ErrNoRows) {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load MFA policy"})
		return
	}
	if factorErr == nil {
		challenge, err := a.startPendingMFA(context.Background(), account.UserID, factor, platform, req.DeviceName, req.DeviceID)
		if err != nil {
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not start MFA challenge"})
			return
		}
		writeJSON(ctx, http.StatusAccepted, map[string]any{"mfa_required": true, "challenge_id": challenge, "factor_kind": factor.Kind})
		return
	}
	a.issueLoginSession(ctx, account.UserID, platform, req.DeviceName, req.DeviceID)
}

func (a *API) startPendingMFA(ctx context.Context, userID string, factor *auth.MFAFactor, platform, deviceName, deviceID string) (string, error) {
	codeHash := ""
	code := ""
	if factor.Kind == "email" || factor.Kind == "phone" {
		var err error
		code, err = auth.NewOTP()
		if err != nil {
			return "", err
		}
		codeHash = auth.HashOTP(a.otpSecret, code)
	}
	id, err := a.users.CreatePendingLogin(ctx, &auth.PendingLogin{UserID: userID, FactorID: factor.ID, PlatformID: platform, DeviceName: deviceName, DeviceID: deviceID, CodeHash: codeHash, ExpiresAt: time.Now().Add(10 * time.Minute)})
	if err != nil {
		return "", err
	}
	if factor.Kind == "email" {
		if err = a.email.Send(ctx, &notify.Email{To: factor.ContactValue, Subject: "PingMessenger login code", Text: "Your login code is " + code + ". It expires in 10 minutes."}); err != nil {
			return "", err
		}
	}
	if factor.Kind == "phone" {
		if a.sms == nil {
			return "", fmt.Errorf("SMS delivery is unavailable")
		}
		if err = a.sms.SendSMS(ctx, &notify.SMS{To: factor.ContactValue, Text: "Your PingMessenger login code is " + code + ". It expires in 10 minutes."}); err != nil {
			return "", err
		}
	}
	return id, nil
}

type loginMFAVerifyRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
}

func (a *API) loginMFAVerify(ctx *fasthttp.RequestCtx) {
	var req loginMFAVerifyRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	pending, err := a.users.PendingLogin(context.Background(), req.ChallengeID)
	if err != nil || pending.Consumed || pending.Attempts >= 5 || time.Now().After(pending.ExpiresAt) {
		writeJSON(ctx, 401, map[string]string{"error": "invalid or expired MFA challenge"})
		return
	}
	factor, err := a.users.MFAFactor(context.Background(), pending.UserID, pending.FactorID)
	if err != nil {
		writeJSON(ctx, 401, map[string]string{"error": "invalid MFA challenge"})
		return
	}
	valid := false
	if factor.Kind == "totp" {
		secret, e := a.deviceTokenCipher.Decrypt(factor.SecretCiphertext)
		valid = e == nil && auth.TOTPMatches(secret, req.Code, time.Now())
	} else {
		valid = auth.OTPMatches(a.otpSecret, req.Code, pending.CodeHash)
	}
	if !valid {
		_ = a.users.RecordPendingLoginFailure(context.Background(), pending.ID)
		writeJSON(ctx, 401, map[string]string{"error": "invalid MFA code"})
		return
	}
	if err = a.users.ConsumePendingLogin(context.Background(), pending.ID); err != nil {
		writeJSON(ctx, 401, map[string]string{"error": "invalid or expired MFA challenge"})
		return
	}
	a.issueLoginSession(ctx, pending.UserID, pending.PlatformID, pending.DeviceName, pending.DeviceID)
}

type loginStartRequest struct {
	Email string `json:"email" validate:"required,email,max=254"`
}

func (a *API) loginStart(ctx *fasthttp.RequestCtx) {
	var req loginStartRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	contact, err := a.users.FindContact(context.Background(), "email", strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil || !contact.Verified {
		writeJSON(ctx, http.StatusAccepted, map[string]string{"status": "if eligible, a verification code was sent"})
		return
	}
	code, err := auth.NewOTP()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create challenge"})
		return
	}
	id, err := a.users.CreateChallenge(context.Background(), contact, "login", auth.HashOTP(a.otpSecret, code), time.Now().Add(10*time.Minute))
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not deliver verification code"})
		return
	}
	if err := a.email.Send(context.Background(), &notify.Email{To: contact.Value, Subject: "PingMessenger login code", Text: "Your login code is " + code + ". It expires in 10 minutes."}); err != nil {
		_ = a.users.InvalidateChallenge(context.Background(), id)
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not deliver verification code"})
		return
	}
	writeJSON(ctx, http.StatusAccepted, map[string]string{"challenge_id": id, "status": "verification code sent"})
}

type loginVerifyRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
	PlatformID  string `json:"platform_id" validate:"required,max=64"`
	DeviceName  string `json:"device_name" validate:"omitempty,max=128"`
	DeviceID    string `json:"device_id" validate:"omitempty,max=128"`
}

func (a *API) loginVerify(ctx *fasthttp.RequestCtx) {
	var req loginVerifyRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	platform, ok := auth.NormalizePlatform(req.PlatformID)
	if !ok {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported platform_id"})
		return
	}
	ch, err := a.users.GetChallenge(context.Background(), req.ChallengeID)
	if err != nil || ch.Purpose != "login" || ch.Consumed || ch.Attempts >= 5 || time.Now().After(ch.ExpiresAt) || !auth.OTPMatches(a.otpSecret, req.Code, ch.CodeHash) {
		if ch != nil {
			_ = a.users.RecordChallengeFailure(context.Background(), ch.ID)
		}
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired challenge"})
		return
	}
	if err = a.users.ConsumeChallenge(context.Background(), ch); err != nil {
		// A parallel verifier may have consumed this OTP after GetChallenge.
		// Treat that race as an invalid credential, never as a server failure.
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired challenge"})
		return
	}
	a.issueLoginSession(ctx, ch.UserID, platform, req.DeviceName, req.DeviceID)
}

func (a *API) issueLoginSession(ctx *fasthttp.RequestCtx, userID, platform, deviceName, deviceID string) {
	refresh, hash, err := a.tokens.NewRefresh()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	session, err := a.users.CreateSession(context.Background(), userID, hash, platform, deviceName, deviceID, time.Now().Add(a.tokens.RefreshTTL()))
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	access, _, err := a.tokens.IssueAccess(userID, session.ID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not issue access token"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"access_token": access, "refresh_token": refresh, "session_id": session.ID})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required,min=32,max=512"`
}

func (a *API) refresh(ctx *fasthttp.RequestCtx) {
	var req refreshRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	raw, nextHash, err := a.tokens.NewRefresh()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not rotate session"})
		return
	}
	session, err := a.users.RotateRefresh(context.Background(), a.tokens.Hash(req.RefreshToken), nextHash, time.Now().Add(a.tokens.RefreshTTL()))
	if err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
		return
	}
	access, _, err := a.tokens.IssueAccess(session.UserID, session.ID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not issue access token"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"access_token": access, "refresh_token": raw, "session_id": session.ID})
}
