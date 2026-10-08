package httpapi

import (
	"context"
	"github.com/fasthttp/router"
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
	r.POST("/v1/auth/login/start", a.loginStart)
	r.POST("/v1/auth/login/verify", a.loginVerify)
	r.POST("/v1/auth/refresh", a.refresh)
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
	refresh, hash, err := a.tokens.NewRefresh()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	session, err := a.users.CreateSession(context.Background(), ch.UserID, hash, platform, req.DeviceName, req.DeviceID, time.Now().Add(a.tokens.RefreshTTL()))
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	access, _, err := a.tokens.IssueAccess(ch.UserID, session.ID)
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
