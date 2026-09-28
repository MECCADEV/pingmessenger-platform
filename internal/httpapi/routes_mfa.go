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

func (a *API) registerMFARoutes(r *router.Router) {
	r.POST("/v1/mfa/challenge", a.issueChallenge)
	r.POST("/v1/mfa/verify", a.verifyChallenge)
}

type verifyChallengeRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
}

func (a *API) verifyChallenge(ctx *fasthttp.RequestCtx) {
	var req verifyChallengeRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	challenge, err := a.users.GetChallenge(context.Background(), req.ChallengeID)
	if challenge != nil && challenge.Purpose == "login" {
		writeJSON(ctx, http.StatusBadRequest, map[string]string{"error": "use /v1/auth/login/verify for login challenges"})
		return
	}
	if err != nil || challenge.Consumed || challenge.Attempts >= 5 || time.Now().After(challenge.ExpiresAt) {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired challenge"})
		return
	}
	if !auth.OTPMatches(a.otpSecret, req.Code, challenge.CodeHash) {
		_ = a.users.RecordChallengeFailure(context.Background(), challenge.ID)
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired challenge"})
		return
	}
	if err = a.users.ConsumeChallenge(context.Background(), challenge); err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired challenge"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"status": "verified", "user_id": challenge.UserID})
}

type challengeRequest struct {
	Email   string `json:"email" validate:"required,email,max=254"`
	Purpose string `json:"purpose" validate:"required,oneof=signup_contact_verification login step_up"`
}

func (a *API) issueChallenge(ctx *fasthttp.RequestCtx) {
	var req challengeRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	contact, err := a.users.FindContact(context.Background(), "email", strings.ToLower(strings.TrimSpace(req.Email)))
	// Do not reveal account existence to an unauthenticated caller.
	if err != nil {
		writeJSON(ctx, http.StatusAccepted, map[string]string{"status": "if eligible, a verification code was sent"})
		return
	}
	code, err := auth.NewOTP()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create challenge"})
		return
	}
	id, err := a.users.CreateChallenge(context.Background(), contact, req.Purpose, auth.HashOTP(a.otpSecret, code), time.Now().Add(10*time.Minute))
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create challenge"})
		return
	}
	if err = a.email.Send(context.Background(), &notify.Email{To: contact.Value, Subject: "PingMessenger verification code", Text: "Your verification code is " + code + ". It expires in 10 minutes."}); err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not deliver verification code"})
		return
	}
	writeJSON(ctx, http.StatusAccepted, map[string]string{"challenge_id": id, "status": "verification code sent"})
}
