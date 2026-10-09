package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/fasthttp/router"
	"github.com/go-playground/validator/v10"
	json "github.com/goccy/go-json"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/notify"
	"pingmessenger/internal/openapi"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/storage"
)

type API struct {
	db                Pinger
	openIM            openim.Client
	users             *auth.UserRepository
	email             notify.EmailSender
	sms               notify.SMSSender
	otpSecret         string
	tokens            *auth.TokenManager
	deviceTokenCipher *auth.DeviceTokenCipher
	objects           storage.ObjectStore
	assetBase         string
	validate          *validator.Validate
}
type Pinger interface{ Ping(context.Context) error }

func New(pool *pgxpool.Pool, openIM openim.Client, email notify.EmailSender, sms notify.SMSSender, otpSecret string, tokens *auth.TokenManager, deviceTokenCipher *auth.DeviceTokenCipher, objects storage.ObjectStore, assetBase string) *API {
	return &API{db: pool, openIM: openIM, users: auth.NewUserRepository(pool), email: email, sms: sms, otpSecret: otpSecret, tokens: tokens, deviceTokenCipher: deviceTokenCipher, objects: objects, assetBase: assetBase, validate: validator.New()}
}

func (a *API) Router() fasthttp.RequestHandler {
	r := router.New()
	r.GET("/healthz", a.health)
	r.GET("/openapi.json", a.openAPIDocument)
	a.registerAuthRoutes(r)
	a.registerUsernameAvailabilityWS(r)
	a.registerPasswordRoutes(r)
	a.registerMFARoutes(r)
	a.registerSecurityRoutes(r)
	a.registerProfileRoutes(r)
	a.registerFriendRoutes(r)
	a.registerOpenIMRoutes(r)
	return r.Handler
}

// openAPIDocument exposes the checked-in Huma-derived schema while leaving
// FastHTTP as the only runtime router and handler implementation.
func (a *API) openAPIDocument(ctx *fasthttp.RequestCtx) {
	document, err := openapi.Document()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not generate OpenAPI document"})
		return
	}
	body, err := document.MarshalJSON()
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not marshal OpenAPI document"})
		return
	}
	ctx.SetStatusCode(http.StatusOK)
	ctx.SetContentType("application/vnd.oai.openapi+json;version=3.1")
	_, _ = ctx.Write(body)
}

type signupRequest struct {
	Username   string `json:"username" validate:"omitempty,min=3,max=64"`
	Password   string `json:"password" validate:"required,min=12,max=256"`
	Nickname   string `json:"nickname" validate:"omitempty,max=128"`
	Email      string `json:"email" validate:"omitempty,email,max=254"`
	Phone      string `json:"phone" validate:"omitempty,max=32"`
	PlatformID string `json:"platform_id" validate:"omitempty,max=64"`
	DeviceName string `json:"device_name" validate:"omitempty,max=128"`
	DeviceID   string `json:"device_id" validate:"omitempty,max=128"`
}
type usernameRequest struct {
	Username string `json:"username" validate:"required,min=3,max=64"`
}

func (a *API) signup(ctx *fasthttp.RequestCtx) {
	var request signupRequest
	if err := a.DecodeAndValidate(ctx, &request); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if request.Email != "" && request.Phone != "" {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "provide at most one contact during signup"})
		return
	}
	platform := request.PlatformID
	if platform == "" {
		platform = "web"
	}
	platform, ok := auth.NormalizePlatform(platform)
	if !ok {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported platform_id"})
		return
	}
	username := strings.ToLower(strings.TrimSpace(request.Username))
	passwordHash, err := auth.HashPassword(request.Password)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not secure password"})
		return
	}
	var kind, value *string
	if request.Email != "" {
		k, v := "email", strings.ToLower(strings.TrimSpace(request.Email))
		kind, value = &k, &v
	}
	if request.Phone != "" {
		k, v := "phone", strings.TrimSpace(request.Phone)
		kind, value = &k, &v
	}
	nickname := strings.TrimSpace(request.Nickname)
	created, err := a.users.Create(context.Background(), &auth.CreateUserParams{Username: username, PasswordHash: passwordHash, Nickname: nickname, ContactKind: kind, ContactValue: value})
	if err != nil {
		var pgError *pgconn.PgError
		if strings.Contains(err.Error(), "duplicate key") || (errors.As(err, &pgError) && pgError.Code == "23505") {
			writeJSON(ctx, http.StatusConflict, map[string]string{"error": "username or contact already exists"})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create account"})
		return
	}
	// OpenIM's deployed storage accepts only alphanumeric user IDs. Keep the
	// platform UUID as the database identity, and derive a stable safe OpenIM
	// identity from it rather than sending UUID hyphens to OpenIM.
	sync := "complete"
	openIMUserID := "pm" + strings.ReplaceAll(created.ID, "-", "")
	openIMNickname := nickname
	if openIMNickname == "" {
		openIMNickname = username
		if openIMNickname == "" {
			openIMNickname = created.PingID
		}
	}
	if _, provisionErr := a.openIM.ProvisionUser(context.Background(), &openim.ProvisionUserRequest{UserID: openIMUserID, Nickname: openIMNickname}); provisionErr != nil || a.users.SetOpenIMUserID(context.Background(), created.ID, openIMUserID) != nil {
		sync = "pending"
	}
	tokens, err := a.newSession(created.ID, platform, request.DeviceName, request.DeviceID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "account created but could not issue session"})
		return
	}
	tokens["user_id"] = created.ID
	tokens["ping_id"] = created.PingID
	tokens["status"] = "active"
	tokens["openim_sync"] = sync
	writeJSON(ctx, http.StatusCreated, tokens)
}

func (a *API) verifyUsername(ctx *fasthttp.RequestCtx) {
	var request usernameRequest
	if err := a.DecodeAndValidate(ctx, &request); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	available, err := a.users.UsernameAvailable(context.Background(), strings.ToLower(strings.TrimSpace(request.Username)))
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not verify username"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]bool{"available": available})
}

func (a *API) health(ctx *fasthttp.RequestCtx) {
	if err := a.db.Ping(context.Background()); err != nil {
		writeJSON(ctx, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"status": "ok"})
}
func (a *API) authenticated(ctx *fasthttp.RequestCtx) (string, string, bool) {
	raw := strings.TrimSpace(string(ctx.Request.Header.Peek("Authorization")))
	if !strings.HasPrefix(raw, "Bearer ") {
		return "", "", false
	}
	claims, err := a.tokens.VerifyAccess(strings.TrimPrefix(raw, "Bearer "))
	if err != nil {
		return "", "", false
	}
	userID, ok1 := claims["sub"].(string)
	sessionID, ok2 := claims["sid"].(string)
	if !ok1 || !ok2 || !a.users.SessionActive(context.Background(), userID, sessionID) {
		return "", "", false
	}
	return userID, sessionID, true
}

// DecodeAndValidate is the only request-body entry point for API handlers.
// It rejects typoed/unknown fields and a second JSON value before validating
// the DTO's structural constraints. Authorization and database checks belong
// to the service layer.
func (a *API) DecodeAndValidate(ctx *fasthttp.RequestCtx, target any) error {
	// FastHTTP commonly buffers request bodies, in which case RequestBodyStream
	// is nil. Decode the stable buffered slice instead of dereferencing it.
	decoder := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain exactly one JSON value")
	}
	if err := a.validate.Struct(target); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}
	return nil
}

func writeJSON(ctx *fasthttp.RequestCtx, status int, body any) {
	ctx.SetStatusCode(status)
	ctx.SetContentType("application/json; charset=utf-8")
	if err := json.NewEncoder(ctx).Encode(body); err != nil {
		ctx.SetStatusCode(http.StatusInternalServerError)
	}
}
