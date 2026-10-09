package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/notify"
)

type passwordRecoveryStartRequest struct {
	Username string `json:"username" validate:"omitempty,min=3,max=64"`
	PingID   string `json:"ping_id" validate:"omitempty,len=12,numeric"`
	Email    string `json:"email" validate:"omitempty,email,max=254"`
	Phone    string `json:"phone" validate:"omitempty,max=32"`
}
type passwordRecoveryCompleteRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
	NewPassword string `json:"new_password" validate:"required,min=12,max=256"`
}
type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password" validate:"required,min=12,max=256"`
	NewPassword     string `json:"new_password" validate:"required,min=12,max=256"`
}
type passwordChangeVerifyRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
	NewPassword string `json:"new_password" validate:"required,min=12,max=256"`
}

func (a *API) registerPasswordRoutes(r interface {
	POST(string, fasthttp.RequestHandler)
}) {
	r.POST("/v1/auth/password/recovery/start", a.passwordRecoveryStart)
	r.POST("/v1/auth/password/recovery/complete", a.passwordRecoveryComplete)
	r.POST("/v1/auth/password/change", a.passwordChange)
	r.POST("/v1/auth/password/change/verify", a.passwordChangeVerify)
}

func (a *API) passwordRecoveryStart(ctx *fasthttp.RequestCtx) {
	var req passwordRecoveryStartRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	identifiers := 0
	kind, value := "", ""
	if req.Username != "" {
		identifiers++
		kind, value = "username", strings.ToLower(strings.TrimSpace(req.Username))
	}
	if req.PingID != "" {
		identifiers++
		kind, value = "ping_id", req.PingID
	}
	if req.Email != "" {
		identifiers++
		kind, value = "email", strings.ToLower(strings.TrimSpace(req.Email))
	}
	if req.Phone != "" {
		identifiers++
		kind, value = "phone", strings.TrimSpace(req.Phone)
	}
	if identifiers != 1 {
		writeJSON(ctx, 422, map[string]string{"error": "provide exactly one recovery identifier"})
		return
	}
	userID, err := a.users.LookupRecoveryUser(context.Background(), kind, value)
	if err == nil {
		contacts, contactErr := a.users.VerifiedContacts(context.Background(), userID)
		if contactErr == nil && len(contacts) > 0 {
			code, codeErr := auth.NewOTP()
			if codeErr == nil {
				challenge, challengeErr := a.users.CreatePasswordChallenge(context.Background(), userID, "recovery", auth.HashOTP(a.otpSecret, code), time.Now().Add(10*time.Minute))
				if challengeErr == nil {
					if sendErr := a.sendPasswordCode(context.Background(), contacts, code, "PingMessenger password recovery code"); sendErr == nil {
						writeJSON(ctx, http.StatusAccepted, map[string]string{"challenge_id": challenge, "status": "verification code sent"})
						return
					}
				}
			}
		}
	}
	// Deliberately do not reveal whether the account, identifier, or contacts exist.
	writeJSON(ctx, http.StatusAccepted, map[string]string{"status": "if eligible, a recovery code was sent"})
}

func (a *API) sendPasswordCode(ctx context.Context, contacts []*auth.Contact, code, subject string) error {
	for _, contact := range contacts {
		var err error
		if contact.Kind == "email" {
			err = a.email.Send(ctx, &notify.Email{To: contact.Value, Subject: subject, Text: "Your PingMessenger code is " + code + ". It expires in 10 minutes."})
		} else if contact.Kind == "phone" && a.sms != nil {
			err = a.sms.SendSMS(ctx, &notify.SMS{To: contact.Value, Text: "Your PingMessenger code is " + code + ". It expires in 10 minutes."})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *API) passwordRecoveryComplete(ctx *fasthttp.RequestCtx) {
	var req passwordRecoveryCompleteRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	challenge, err := a.users.PasswordChallenge(context.Background(), req.ChallengeID)
	if err != nil || challenge.Purpose != "recovery" || challenge.Consumed || challenge.Attempts >= 5 || time.Now().After(challenge.ExpiresAt) || !auth.OTPMatches(a.otpSecret, req.Code, challenge.CodeHash) {
		if challenge != nil {
			_ = a.users.RecordPasswordChallengeFailure(context.Background(), challenge.ID)
		}
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired recovery challenge"})
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil || a.users.ConsumePasswordChallenge(context.Background(), challenge.ID) != nil || a.users.ChangePassword(context.Background(), challenge.UserID, hash) != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "could not reset password"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"status": "password changed; sessions revoked"})
}

func (a *API) passwordChange(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var req passwordChangeRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	account, err := a.users.PasswordLoginByID(context.Background(), userID)
	if err != nil || !auth.VerifyPassword(req.CurrentPassword, account.PasswordHash) {
		writeJSON(ctx, 401, map[string]string{"error": "invalid current password"})
		return
	}
	contacts, err := a.users.VerifiedContacts(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not load contacts"})
		return
	}
	if len(contacts) == 0 {
		hash, e := auth.HashPassword(req.NewPassword)
		if e != nil || a.users.ChangePassword(context.Background(), userID, hash) != nil {
			writeJSON(ctx, 500, map[string]string{"error": "could not change password"})
			return
		}
		writeJSON(ctx, 200, map[string]string{"status": "password changed; sessions revoked"})
		return
	}
	code, err := auth.NewOTP()
	if err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not create challenge"})
		return
	}
	challenge, err := a.users.CreatePasswordChallenge(context.Background(), userID, "change", auth.HashOTP(a.otpSecret, code), time.Now().Add(10*time.Minute))
	if err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not create challenge"})
		return
	}
	if err = a.sendPasswordCode(context.Background(), contacts, code, "PingMessenger password change code"); err != nil {
		writeJSON(ctx, 502, map[string]string{"error": "could not deliver challenge"})
		return
	}
	writeJSON(ctx, 202, map[string]string{"challenge_id": challenge, "status": "verification code sent"})
}

func (a *API) passwordChangeVerify(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var req passwordChangeVerifyRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	challenge, err := a.users.PasswordChallenge(context.Background(), req.ChallengeID)
	if err != nil || challenge.UserID != userID || challenge.Purpose != "change" || challenge.Consumed || challenge.Attempts >= 5 || time.Now().After(challenge.ExpiresAt) || !auth.OTPMatches(a.otpSecret, req.Code, challenge.CodeHash) {
		if challenge != nil {
			_ = a.users.RecordPasswordChallengeFailure(context.Background(), challenge.ID)
		}
		writeJSON(ctx, 401, map[string]string{"error": "invalid or expired password challenge"})
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil || a.users.ConsumePasswordChallenge(context.Background(), challenge.ID) != nil || a.users.ChangePassword(context.Background(), userID, hash) != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not change password"})
		return
	}
	writeJSON(ctx, 200, map[string]string{"status": "password changed; sessions revoked"})
}
