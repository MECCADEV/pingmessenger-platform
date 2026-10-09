package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/fasthttp/router"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/valyala/fasthttp"
	"net/http"
	"path"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/storage"
	"strings"
)

func (a *API) registerProfileRoutes(r *router.Router) {
	r.GET("/v1/profile/", a.profile)
	r.PATCH("/v1/profile/username", a.updateUsername)
	r.POST("/v1/profile/update", a.updateProfile)
}
func (a *API) profile(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	p, err := a.users.Profile(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load profile"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"username": p.Username, "ping_id": p.PingID, "image": p.Image, "nickname": p.Nickname})
}

func (a *API) updateUsername(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req usernameRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if err := a.users.UpdateUsername(context.Background(), userID, username); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSON(ctx, http.StatusConflict, map[string]string{"error": "username is already in use"})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not update username"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"username": username})
}
func (a *API) updateProfile(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	form, formErr := ctx.Request.MultipartForm()
	if formErr != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "multipart/form-data is required"})
		return
	}
	defer ctx.Request.RemoveMultipartFormFiles()
	nicknameValues, nicknameProvided := form.Value["nickname"]
	nickname := ""
	if nicknameProvided {
		if len(nicknameValues) != 1 {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "nickname must be provided once"})
			return
		}
		nickname = strings.TrimSpace(nicknameValues[0])
		if len(nickname) > 128 {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "nickname must be at most 128 characters"})
			return
		}
	}
	file, fileErr := ctx.FormFile("image")
	if fileErr != nil && !nicknameProvided {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "image or nickname multipart field is required"})
		return
	}
	var public *string
	if fileErr == nil {
		stream, err := file.Open()
		if err != nil {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "could not read image"})
			return
		}
		defer stream.Close()
		contentType := file.Header.Get("Content-Type")
		if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "image must be jpeg, png, or webp"})
			return
		}
		data, err := storage.ReadLimited(stream, 5<<20)
		if err != nil {
			writeJSON(ctx, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
			return
		}
		id := make([]byte, 16)
		if _, err = rand.Read(id); err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create asset"})
			return
		}
		key := path.Join("profiles", userID, hex.EncodeToString(id))
		if err = a.objects.Put(context.Background(), key, contentType, data); err != nil {
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not store image"})
			return
		}
		image := storage.PublicPath(a.assetBase, key)
		if err = a.users.StoreProfileAsset(context.Background(), userID, key, image, contentType, int64(len(data))); err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not store image metadata"})
			return
		}
		public = &image
	}
	if nicknameProvided {
		if err := a.users.UpdateNickname(context.Background(), userID, nickname); err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not update nickname"})
			return
		}
	}
	effectiveNickname := nickname
	if !nicknameProvided {
		profile, err := a.users.Profile(context.Background(), userID)
		if err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load profile"})
			return
		}
		effectiveNickname = profile.Nickname
	}
	openIMUserID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusAccepted, map[string]any{"image": public, "nickname": effectiveNickname, "sync": "pending"})
		return
	}
	var openIMNickname *string
	if nicknameProvided {
		openIMNickname = &nickname
	}
	if err = a.openIM.UpdateProfile(context.Background(), &openim.UpdateProfileRequest{UserID: openIMUserID, Nickname: openIMNickname, FaceURL: public}); err != nil {
		writeJSON(ctx, http.StatusAccepted, map[string]any{"image": public, "nickname": effectiveNickname, "sync": "pending"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"image": public, "nickname": effectiveNickname, "sync": "complete"})
}
