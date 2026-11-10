package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/notify"
	"pingmessenger/internal/openim"
)

type passwordRecoveryOptionsRequest struct {
	PingID string `json:"ping_id" validate:"required,len=12,numeric"`
}
type passwordRecoveryStartRequest struct {
	PingID  string `json:"ping_id" validate:"required,len=12,numeric"`
	Channel string `json:"channel" validate:"required,oneof=email phone"`
}
type passwordRecoveryVerifyRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
}
type passwordRecoveryCompleteRequest struct {
	ResetToken  string `json:"reset_token" validate:"required,min=32,max=128"`
	NewPassword string `json:"new_password" validate:"required,min=6,max=256"`
}
type passwordChangeRequest struct {
	// The current password is deliberately not subject to the new-password
	// strength rule. A short legacy password must still authenticate and a
	// mismatch is always classified as 401 rather than validation failure.
	CurrentPassword string `json:"current_password" validate:"required,max=256"`
	NewPassword     string `json:"new_password" validate:"required,min=6,max=256"`
}
type passwordChangeVerifyRequest struct {
	ChallengeID string `json:"challenge_id" validate:"required,uuid4"`
	Code        string `json:"code" validate:"required,len=6,numeric"`
	NewPassword string `json:"new_password" validate:"required,min=6,max=256"`
}

func (a *API) registerPasswordRoutes(r interface {
	POST(string, fasthttp.RequestHandler)
}) {
	r.POST("/v1/auth/password/recovery/options", a.passwordRecoveryOptions)
	r.POST("/v1/auth/password/recovery/start", a.passwordRecoveryStart)
	r.POST("/v1/auth/password/recovery/verify", a.passwordRecoveryVerify)
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
	userID, contact, err := a.users.VerifiedRecoveryContactByPingID(context.Background(), req.PingID, req.Channel)
	if err != nil || userID == "" || contact == nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "no verified recovery contact for that channel"})
		return
	}
	if retryAfter, err := a.users.ReserveMFAChallenge(context.Background(), contact.ID, req.Channel+":"+contact.Value, requestIP(ctx), time.Now()); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not apply recovery rate limit"})
		return
	} else if retryAfter > 0 {
		ctx.Response.Header.Set("Retry-After", strconv.Itoa(int((retryAfter+time.Second-1)/time.Second)))
		writeJSON(ctx, http.StatusTooManyRequests, map[string]string{"error": "too many recovery requests"})
		return
	}
	code, err := auth.NewOTP()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create recovery challenge"})
		return
	}
	challengeID, err := a.users.CreatePasswordChallenge(context.Background(), userID, "recovery", auth.HashOTP(a.otpSecret, code), time.Now().Add(10*time.Minute))
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create recovery challenge"})
		return
	}
	if err = a.sendPasswordCode(context.Background(), []*auth.Contact{contact}, code, "PingMessenger password recovery code"); err != nil {
		_ = a.users.InvalidatePasswordChallenge(context.Background(), challengeID)
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not deliver recovery code"})
		return
	}
	writeJSON(ctx, http.StatusAccepted, map[string]string{"challenge_id": challengeID})
}

func (a *API) passwordRecoveryOptions(ctx *fasthttp.RequestCtx) {
	var req passwordRecoveryOptionsRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if !a.enforceRate(ctx, "recovery-options", req.PingID, 20, 60) {
		return
	}
	options, err := a.users.RecoveryOptionsForPingID(context.Background(), req.PingID)
	if err != nil || options == nil {
		writeJSON(ctx, http.StatusOK, map[string]any{"email": nil, "phone": nil})
		return
	}
	var email, phone *string
	if options.Email != nil {
		value := maskRecoveryEmail(*options.Email)
		email = &value
	}
	if options.Phone != nil {
		value := maskRecoveryPhone(*options.Phone)
		phone = &value
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"email": email, "phone": phone})
}

func maskRecoveryEmail(value string) string {
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	return string([]rune(parts[0])[0]) + "***@" + parts[1]
}

func maskRecoveryPhone(value string) string {
	runes := []rune(value)
	if len(runes) <= 2 {
		return "**"
	}
	return strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-2:])
}

func (a *API) sendPasswordCode(ctx context.Context, contacts []*auth.Contact, code, subject string) error {
	for _, contact := range contacts {
		var err error
		if contact.Kind == "email" {
			err = a.email.Send(ctx, &notify.Email{To: contact.Value, Subject: subject, Text: "Your PingMessenger code is " + code + ". It expires in 10 minutes."})
		} else if contact.Kind == "phone" && a.sms != nil {
			phone, normalizeErr := normalizeContactValue("phone", contact.Value)
			if normalizeErr != nil {
				return normalizeErr
			}
			err = a.sms.SendSMS(ctx, &notify.SMS{To: phone, Text: "Your PingMessenger code is " + code + ". It expires in 10 minutes."})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *API) passwordRecoveryVerify(ctx *fasthttp.RequestCtx) {
	var req passwordRecoveryVerifyRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
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
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not issue reset token"})
		return
	}
	resetToken := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if err = a.users.ConsumeRecoveryChallengeAndCreateResetToken(context.Background(), challenge.ID, auth.HashOTP(a.otpSecret, resetToken), time.Now().Add(15*time.Minute)); err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired recovery challenge"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"reset_token": resetToken})
}

func (a *API) passwordRecoveryComplete(ctx *fasthttp.RequestCtx) {
	var req passwordRecoveryCompleteRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, 422, map[string]string{"error": err.Error()})
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired reset token"})
		return
	}
	openIMUserID, platforms, err := a.users.CompletePasswordReset(context.Background(), auth.HashOTP(a.otpSecret, req.ResetToken), hash)
	if err != nil {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "invalid or expired reset token"})
		return
	}
	openIMRevoked := true
	if openIMUserID != "" {
		for _, platform := range platforms {
			platformID, ok := openIMPlatform(platform)
			if !ok || a.openIM.ForceLogout(context.Background(), &openim.ForceLogoutRequest{UserID: openIMUserID, PlatformID: platformID}) != nil {
				openIMRevoked = false
			}
		}
	}
	status := http.StatusOK
	message := "password changed; all sessions revoked"
	if !openIMRevoked {
		message = "password changed; some OpenIM sessions could not be revoked"
	}
	writeJSON(ctx, status, map[string]any{"status": message, "openim_sessions_revoked": openIMRevoked})
}

func (a *API) passwordChange(ctx *fasthttp.RequestCtx) {
	userID, sessionID, ok := a.authenticated(ctx)
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
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not secure password"})
		return
	}
	if err = a.users.ChangePasswordKeepSession(context.Background(), userID, sessionID, hash); err != nil {
		writeJSON(ctx, 500, map[string]string{"error": "could not change password"})
		return
	}
	writeJSON(ctx, 200, map[string]string{"status": "password changed"})
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
