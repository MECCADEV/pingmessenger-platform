package httpapi

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/documents"
	"pingmessenger/internal/storage"
)

func (a *API) registerDocumentRoutes(r *router.Router) {
	r.POST("/v1/documents", a.uploadDocument)
	r.GET("/v1/documents/{documentID}", a.getDocument)
	r.GET("/v1/documents/{documentID}/download", a.downloadDocument)
	r.DELETE("/v1/documents/{documentID}", a.deleteDocument)
	r.POST("/v1/documents/{documentID}/message-link", a.linkDocumentMessage)
}

type documentMessageLinkRequest struct {
	ConversationID string `json:"conversation_id" validate:"required,max=256"`
	Seq            int64  `json:"seq" validate:"required,gt=0"`
}

func (a *API) uploadDocument(ctx *fasthttp.RequestCtx) {
	ownerID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "document-upload", ownerID, documentUploadUserLimit, documentUploadIPLimit) {
		return
	}
	_, err := ctx.Request.MultipartForm()
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "multipart/form-data is required"})
		return
	}
	defer ctx.Request.RemoveMultipartFormFiles()
	file, err := ctx.FormFile("document")
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "document is required"})
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(file.Header.Get("Content-Type")))
	if _, allowed := a.documentPolicy.AllowedMIMEs[contentType]; !allowed {
		writeJSON(ctx, http.StatusUnsupportedMediaType, map[string]string{"error": "unsupported document content type"})
		return
	}
	if file.Size > a.documentPolicy.MaxBytes {
		writeJSON(ctx, http.StatusRequestEntityTooLarge, map[string]string{"error": "document exceeds the maximum size"})
		return
	}
	stream, err := file.Open()
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "could not read document"})
		return
	}
	defer stream.Close()
	data, err := storage.ReadLimited(stream, a.documentPolicy.MaxBytes)
	if err != nil {
		writeJSON(ctx, http.StatusRequestEntityTooLarge, map[string]string{"error": "document exceeds the maximum size"})
		return
	}
	name := safeDocumentName(file.Filename)
	if name == "" {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "document filename is invalid"})
		return
	}
	key := path.Join("message-documents", ownerID, uuid.NewString())
	doc, err := a.documents.Reserve(context.Background(), ownerID, key, name, contentType, int64(len(data)), a.documentPolicy)
	if err != nil {
		switch {
		case errors.Is(err, documents.ErrCountLimit), errors.Is(err, documents.ErrQuotaLimit):
			writeJSON(ctx, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		default:
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not reserve document"})
		}
		return
	}
	if err = a.documentObjects.Put(context.Background(), key, contentType, data); err != nil {
		a.abandonDocument(context.Background(), ownerID, doc.ID, err)
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not store document"})
		return
	}
	if !a.enforceRate(ctx, "document-signed-url", ownerID, signedURLUserLimit, signedURLIPLimit) {
		// The row was reserved before the second limiter. Mark it deleted before
		// removing the object; otherwise a rejected request leaves an active
		// database row pointing at a deleted object.
		a.abandonDocument(context.Background(), ownerID, doc.ID, errors.New("signed URL rate limit exceeded"))
		return
	}
	doc.SourceURL, err = a.documentSourceURL(context.Background(), key)
	if err != nil {
		a.abandonDocument(context.Background(), ownerID, doc.ID, err)
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not sign document URL"})
		return
	}
	writeJSON(ctx, http.StatusCreated, map[string]any{
		"document": doc,
		"fileElem": map[string]any{"sourceUrl": doc.SourceURL, "fileName": doc.FileName, "fileSize": doc.ByteSize, "uuid": doc.ID},
	})
}

func (a *API) getDocument(ctx *fasthttp.RequestCtx) {
	ownerID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	// Charge signed-URL issuance before the ownership lookup so repeated
	// guesses cannot bypass the issuance budget through database work.
	if !a.enforceRate(ctx, "document-signed-url", ownerID, signedURLUserLimit, signedURLIPLimit) {
		return
	}
	doc, err := a.documents.Get(context.Background(), ownerID, documentID(ctx))
	if errors.Is(err, documents.ErrNotFound) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "document not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load document"})
		return
	}
	if doc.DeletedAt != nil {
		writeJSON(ctx, http.StatusGone, map[string]string{"error": "document has expired"})
		return
	}
	doc.SourceURL, err = a.documentSourceURL(context.Background(), doc.StorageKey)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not sign document URL"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"document": doc})
}

func (a *API) downloadDocument(ctx *fasthttp.RequestCtx) {
	ownerID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "document-download", ownerID, downloadUserLimit, downloadIPLimit) {
		return
	}
	doc, err := a.documents.Get(context.Background(), ownerID, documentID(ctx))
	if errors.Is(err, documents.ErrNotFound) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "document not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load document"})
		return
	}
	if doc.DeletedAt != nil {
		writeJSON(ctx, http.StatusGone, map[string]string{"error": "document has expired"})
		return
	}
	reader, ok := a.documentObjects.(storage.ObjectReader)
	if !ok {
		writeJSON(ctx, http.StatusNotImplemented, map[string]string{"error": "document download proxy is unavailable"})
		return
	}
	body, contentType, err := reader.Get(context.Background(), doc.StorageKey)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not download document"})
		return
	}
	ctx.Response.Header.SetContentType(contentType)
	ctx.Response.SetBodyStream(body, int(doc.ByteSize))
}

func (a *API) deleteDocument(ctx *fasthttp.RequestCtx) {
	ownerID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "document-delete", ownerID, documentDeleteUserLimit, documentDeleteIPLimit) {
		return
	}
	item, err := a.documents.MarkDeleteRequested(context.Background(), ownerID, documentID(ctx))
	if errors.Is(err, documents.ErrNotFound) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "document not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not delete document"})
		return
	}
	if err = a.deleteDocumentObject(context.Background(), *item); err != nil {
		writeJSON(ctx, http.StatusAccepted, map[string]string{"status": "pending_cleanup"})
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

// linkDocumentMessage binds an uploaded document to the OpenIM sequence that
// carried it. The cleanup worker uses this coordinate to revoke the message
// before deleting its S3 object when disappearing-message retention expires.
func (a *API) linkDocumentMessage(ctx *fasthttp.RequestCtx) {
	ownerID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "document-message-link", ownerID, documentLinkUserLimit, documentLinkIPLimit) {
		return
	}
	var req documentMessageLinkRequest
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	conversation := strings.TrimSpace(req.ConversationID)
	setting, err := a.disappearing.Get(context.Background(), ownerID, conversation)
	if err == nil && (!setting.Enabled || setting.DurationSeconds == 0) {
		writeJSON(ctx, http.StatusConflict, map[string]string{"error": "disappearing messages are disabled for this conversation"})
		return
	}
	duration := 86400
	if err == nil {
		duration = setting.DurationSeconds
	} else if timer, timerErr := a.timers.Get(context.Background(), "disappearing_message_default"); timerErr == nil {
		duration = timer.DurationSeconds
	}
	if duration <= 0 {
		writeJSON(ctx, http.StatusConflict, map[string]string{"error": "disappearing messages are disabled"})
		return
	}
	openIMUserID, err := a.users.OpenIMUserID(context.Background(), ownerID)
	if err != nil || openIMUserID == "" {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	expiresAt := time.Now().Add(time.Duration(duration) * time.Second)
	if err = a.documents.LinkMessage(context.Background(), ownerID, documentID(ctx), conversation, openIMUserID, req.Seq, expiresAt); err != nil {
		if errors.Is(err, documents.ErrNotFound) {
			writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "document not found"})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not link document message"})
		return
	}
	writeJSON(ctx, http.StatusCreated, map[string]any{"status": "linked", "expires_at": expiresAt})
}

func documentID(ctx *fasthttp.RequestCtx) string {
	value, _ := ctx.UserValue("documentID").(string)
	return value
}

func (a *API) documentSourceURL(ctx context.Context, key string) (string, error) {
	if presigner, ok := a.documentObjects.(interface {
		PresignGet(context.Context, string, time.Duration) (string, error)
	}); ok {
		// The URL is carried in OpenIM history, so it must remain usable for the
		// whole retention window. AWS S3 caps SigV4 presigned GETs at seven days.
		return presigner.PresignGet(ctx, key, 7*24*time.Hour)
	}
	return storage.PublicPath(a.assetBase, key), nil
}

func (a *API) deleteDocumentObject(ctx context.Context, item documents.CleanupItem) error {
	if err := a.documentObjects.Delete(ctx, item.StorageKey); err != nil {
		_ = a.documents.RecordDeleteError(ctx, item.ID, err.Error())
		return err
	}
	return a.documents.MarkS3Deleted(ctx, item.ID)
}

func (a *API) abandonDocument(ctx context.Context, ownerID, id string, cause error) {
	item, err := a.documents.MarkDeleteRequested(ctx, ownerID, id)
	if err != nil {
		_ = a.documents.RecordDeleteError(ctx, id, cause.Error())
		return
	}
	if err := a.deleteDocumentObject(ctx, *item); err != nil {
		// deleteDocumentObject already records the cleanup failure and releases
		// the claim lease for retry; do not increment the attempt twice.
		return
	}
}

func safeDocumentName(name string) string {
	name = path.Base(strings.TrimSpace(name))
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 255 || name == "." || name == ".." {
		return ""
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return name
}
