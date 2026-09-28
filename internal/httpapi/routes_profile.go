package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"net/http"
	"path"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/storage"
)

func (a *API) registerProfileRoutes(r *router.Router) {
	r.GET("/v1/profile/", a.profile)
	r.POST("/v1/profile/update", a.updateProfile)
}
func (a *API) profile(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	p, err := a.users.ProfilePath(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusOK, map[string]any{"image": nil})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"image": p})
}
func (a *API) updateProfile(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	file, err := ctx.FormFile("image")
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "image multipart field is required"})
		return
	}
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
	public := storage.PublicPath(a.assetBase, key)
	if err = a.users.StoreProfileAsset(context.Background(), userID, key, public, contentType, int64(len(data))); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not store image metadata"})
		return
	}
	openIMUserID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusAccepted, map[string]string{"image": public, "sync": "pending"})
		return
	}
	if err = a.openIM.UpdateProfile(context.Background(), &openim.UpdateProfileRequest{UserID: openIMUserID, FaceURL: &public}); err != nil {
		writeJSON(ctx, http.StatusAccepted, map[string]string{"image": public, "sync": "pending"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]string{"image": public, "sync": "complete"})
}
