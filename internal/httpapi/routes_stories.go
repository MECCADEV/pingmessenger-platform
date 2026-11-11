package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/fasthttp/websocket"
	json "github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/storage"
	"pingmessenger/internal/stories"
)

const storyMaxBytes int64 = 10 << 20

type storyViewRequest struct {
	Reaction string `json:"reaction" validate:"omitempty,max=64"`
}

func (a *API) registerStoryRoutes(r *router.Router) {
	r.POST("/v1/stories", a.createStory)
	r.DELETE("/v1/stories/{storyID}", a.deleteStory)
	r.GET("/v1/stories/{storyID}/media", a.downloadStoryMedia)
	r.POST("/v1/stories/{storyID}/view", a.viewStory)
	r.GET("/v1/stories/{storyID}/views", a.listStoryViews)
	r.GET("/v1/stories/ws", a.storyWebSocket)
}

func (a *API) downloadStoryMedia(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "story-download", userID, downloadUserLimit, downloadIPLimit) {
		return
	}
	story, err := a.stories.MediaForRecipient(context.Background(), userID, strings.TrimSpace(ctx.UserValue("storyID").(string)))
	if errors.Is(err, stories.ErrForbidden) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "story not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load story media"})
		return
	}
	reader, ok := a.objects.(storage.ObjectReader)
	if !ok {
		writeJSON(ctx, http.StatusNotImplemented, map[string]string{"error": "story download proxy is unavailable"})
		return
	}
	body, contentType, err := reader.Get(context.Background(), story.StorageKey)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not download story media"})
		return
	}
	ctx.Response.Header.SetContentType(contentType)
	ctx.Response.SetBodyStream(body, int(story.ByteSize))
}

func (a *API) createStory(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	idempotencyKey, err := parseStoryIdempotencyKey(string(ctx.Request.Header.Peek("Idempotency-Key")))
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if !a.enforceRate(ctx, "story-upload", userID, storyUploadUserLimit, storyUploadIPLimit) {
		return
	}
	// The global server body ceiling is sized for documents; cap story
	// multipart input before parsing to the media limit plus multipart overhead.
	if int64(len(ctx.PostBody())) > storyMaxBytes+(1<<20) {
		writeJSON(ctx, http.StatusRequestEntityTooLarge, map[string]string{"error": "story upload exceeds the maximum request size"})
		return
	}
	form, err := ctx.Request.MultipartForm()
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "multipart/form-data is required"})
		return
	}
	defer ctx.Request.RemoveMultipartFormFiles()
	statusType := strings.ToLower(strings.TrimSpace(firstForm(form.Value, "type")))
	file, fileErr := ctx.FormFile("media")
	var mediaType stories.MediaType
	var contentType string
	var data []byte
	var hasMedia bool
	if fileErr == nil {
		hasMedia = true
		contentType = strings.ToLower(strings.TrimSpace(file.Header.Get("Content-Type")))
		var validMediaType bool
		mediaType, validMediaType = storyMediaType(contentType)
		if !validMediaType {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "media must be an image or video"})
			return
		}
		if statusType != "" && statusType != string(mediaType) {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "type does not match media"})
			return
		}
		stream, err := file.Open()
		if err != nil {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "could not read media"})
			return
		}
		defer stream.Close()
		data, err = storage.ReadLimited(stream, storyMaxBytes)
		if err != nil {
			writeJSON(ctx, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
			return
		}
	} else if statusType != string(stories.MediaText) {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "media is required unless type=text"})
		return
	}
	caption := strings.TrimSpace(firstForm(form.Value, "caption"))
	if len(caption) > 4096 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "caption must be at most 4096 characters"})
		return
	}
	textContent := strings.TrimSpace(firstForm(form.Value, "text"))
	backgroundColor := strings.TrimSpace(firstForm(form.Value, "background_color"))
	if !hasMedia {
		if textContent == "" {
			// `caption` is retained as a client-compatible alias for text-only
			// statuses, but the canonical response field is `text`.
			textContent = caption
		}
		if len(textContent) == 0 || len([]rune(textContent)) > 700 {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "text must be between 1 and 700 characters"})
			return
		}
		if !validStoryBackgroundColor(backgroundColor) {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "background_color must be #RRGGBB for text statuses"})
			return
		}
		mediaType = stories.MediaText
		contentType = "text/plain"
		caption = textContent
	} else if textContent != "" || backgroundColor != "" {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "text and background_color are only valid for type=text"})
		return
	}
	visibility := strings.TrimSpace(firstForm(form.Value, "visibility"))
	if visibility == "" {
		visibility = string(stories.VisibilityContacts)
	}
	if visibility != string(stories.VisibilityContacts) && visibility != string(stories.VisibilityAllow) && visibility != string(stories.VisibilityExclude) {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "visibility must be contacts, allow, or exclude"})
		return
	}
	ids, err := parseStoryIDs(firstForm(form.Value, "user_ids"))
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	expires := 24 * time.Hour
	if raw := firstForm(form.Value, "expires_in_seconds"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 60 || n > 7*24*60*60 {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "expires_in_seconds must be between 60 and 604800"})
			return
		}
		expires = time.Duration(n) * time.Second
	}
	var requestHash string
	if idempotencyKey != "" {
		requestHash, err = hashStoryCreateRequest(string(mediaType), contentType, data, caption, textContent, backgroundColor, visibility, ids, int64(expires/time.Second))
		if err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not fingerprint story request"})
			return
		}
		existing, lookupErr := a.stories.FindIdempotent(context.Background(), userID, idempotencyKey, requestHash)
		if errors.Is(lookupErr, stories.ErrIdempotencyConflict) {
			writeJSON(ctx, http.StatusConflict, map[string]string{"error": lookupErr.Error()})
			return
		}
		if errors.Is(lookupErr, stories.ErrIdempotencyGone) {
			writeJSON(ctx, http.StatusGone, map[string]string{"error": lookupErr.Error()})
			return
		}
		if lookupErr != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load idempotent story result"})
			return
		}
		if existing != nil {
			if existing.StorageKey != "" {
				if !a.enforceRate(ctx, "story-signed-url", userID, signedURLUserLimit, signedURLIPLimit) {
					return
				}
				if err := a.signStoryMedia(context.Background(), existing); err != nil {
					writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not sign story media"})
					return
				}
			}
			ctx.Response.Header.Set("Idempotency-Replayed", "true")
			writeJSON(ctx, http.StatusCreated, map[string]any{"story": existing})
			return
		}
	}
	openID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	friends, err := a.storyFriends(context.Background(), userID, openID)
	if err != nil {
		log.Printf("story contacts lookup failed for user %s: %v", userID, err)
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not load story contacts"})
		return
	}
	discoveredOpenIDs, err := a.users.DiscoveredOpenIMIDs(context.Background(), userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load discovered story contacts"})
		return
	}
	discoveredPlatformIDs, err := a.users.PlatformUserIDsByOpenIM(context.Background(), discoveredOpenIDs)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not resolve discovered story contacts"})
		return
	}
	friendSet := storyAudienceSet(userID, friends, mapValues(discoveredPlatformIDs))
	recipients := make([]string, 0, len(friendSet))
	switch visibility {
	case string(stories.VisibilityContacts):
		for id := range friendSet {
			recipients = append(recipients, id)
		}
	case string(stories.VisibilityAllow):
		for _, id := range ids {
			if _, ok := friendSet[id]; ok {
				recipients = append(recipients, id)
			}
		}
	case string(stories.VisibilityExclude):
		for id := range friendSet {
			excluded := false
			for _, x := range ids {
				if x == id {
					excluded = true
					break
				}
			}
			if !excluded {
				recipients = append(recipients, id)
			}
		}
	}
	key := ""
	privateStory := &stories.Story{}
	if hasMedia {
		randomID := make([]byte, 16)
		if _, err = rand.Read(randomID); err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create story"})
			return
		}
		key = path.Join("stories", userID, hex.EncodeToString(randomID))
		if err = a.objects.Put(context.Background(), key, contentType, data); err != nil {
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not store story media"})
			return
		}
		// Sign once before publishing the story so an unavailable private signer
		// cannot create a story that clients can never download.
		if !a.enforceRate(ctx, "story-signed-url", userID, signedURLUserLimit, signedURLIPLimit) {
			_ = a.objects.Delete(context.Background(), key)
			return
		}
		privateStory.StorageKey = key
		if err := a.signStoryMedia(context.Background(), privateStory); err != nil {
			_ = a.objects.Delete(context.Background(), key)
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not sign story media"})
			return
		}
	}
	story, created, err := a.stories.CreateIdempotent(context.Background(), stories.CreateParams{OwnerID: userID, MediaType: string(mediaType), StorageKey: key, MediaURL: "", ContentType: contentType, ByteSize: int64(len(data)), Caption: caption, Text: textContent, BackgroundColor: backgroundColor, Visibility: visibility, ExpiresAt: time.Now().Add(expires), Recipients: recipients, VisibilityUsers: ids, IdempotencyKey: idempotencyKey, RequestHash: requestHash})
	if err != nil {
		if hasMedia {
			_ = a.objects.Delete(context.Background(), key)
		}
		if errors.Is(err, stories.ErrActiveLimit) {
			writeJSON(ctx, http.StatusTooManyRequests, map[string]string{"error": "maximum of 10 active stories per user reached"})
			return
		}
		if errors.Is(err, stories.ErrTotalLimit) {
			writeJSON(ctx, http.StatusTooManyRequests, map[string]string{"error": "maximum of 40 retained stories per user reached"})
			return
		}
		if errors.Is(err, stories.ErrIdempotencyConflict) {
			writeJSON(ctx, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, stories.ErrIdempotencyGone) {
			writeJSON(ctx, http.StatusGone, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not persist story"})
		return
	}
	if !created {
		if hasMedia && key != story.StorageKey {
			if deleteErr := a.objects.Delete(context.Background(), key); deleteErr != nil {
				log.Printf("failed to delete losing idempotent story upload %s: %v", key, deleteErr)
			}
		}
		if story.StorageKey != "" {
			if err := a.signStoryMedia(context.Background(), story); err != nil {
				writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not sign story media"})
				return
			}
		}
		ctx.Response.Header.Set("Idempotency-Replayed", "true")
	} else {
		story.MediaURL = privateStory.MediaURL
	}
	writeJSON(ctx, http.StatusCreated, map[string]any{"story": story})
}

func storyAudienceSet(ownerID string, friendIDs, discoveredIDs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(friendIDs)+len(discoveredIDs))
	for _, id := range append(append([]string(nil), friendIDs...), discoveredIDs...) {
		if id != "" && id != ownerID {
			set[id] = struct{}{}
		}
	}
	return set
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func validStoryBackgroundColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, c := range value[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func storyMediaType(contentType string) (stories.MediaType, bool) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	switch {
	case strings.HasPrefix(contentType, "image/") && len(contentType) > len("image/"):
		return stories.MediaImage, true
	case strings.HasPrefix(contentType, "video/") && len(contentType) > len("video/"):
		return stories.MediaVideo, true
	default:
		return "", false
	}
}

// storyFriends reads OpenIM's current relation list for each story create.
// Conversation history and process-local caches are not a source of truth for
// the contacts audience.
func (a *API) storyFriends(ctx context.Context, platformID, openIMID string) ([]string, error) {
	openIDs, err := a.openIM.ListFriendIDs(ctx, &openim.ListFriendIDsRequest{UserID: openIMID})
	if err != nil {
		return nil, err
	}
	mapped, err := a.users.PlatformUserIDsByOpenIM(ctx, openIDs)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(mapped))
	for _, id := range mapped {
		if id != platformID {
			set[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids, nil
}

// HandleOpenIMWebhook is the platform-side callback boundary used by OpenIM's
// webhook gRPC transport. Story audience membership is resolved from OpenIM
// and the persisted discovered-contact table, so callbacks need no process
// local graph mutation.
func (a *API) HandleOpenIMWebhook(ctx context.Context, command string, payload []byte) ([]byte, error) {
	return []byte(`{"errCode":0,"errMsg":""}`), nil
}

func firstForm(values map[string][]string, key string) string {
	if v := values[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}
func parseStoryIDs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ids []string
	if strings.HasPrefix(strings.TrimSpace(raw), "[") {
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return nil, fmt.Errorf("user_ids must be a JSON array")
		}
	} else {
		for _, v := range strings.Split(raw, ",") {
			if strings.TrimSpace(v) != "" {
				ids = append(ids, strings.TrimSpace(v))
			}
		}
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("user_ids contains an invalid user id")
		}
	}
	return ids, nil
}

func (a *API) deleteStory(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "story-delete", userID, storyDeleteUserLimit, storyDeleteIPLimit) {
		return
	}
	story, err := a.stories.Delete(context.Background(), userID, strings.TrimSpace(ctx.UserValue("storyID").(string)))
	if errors.Is(err, stories.ErrNotFound) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "story not found"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not delete story"})
		return
	}
	a.deleteStoryMedia(context.Background(), story.ID, story.StorageKey)
	writeJSON(ctx, http.StatusOK, map[string]any{"story": story})
}

func (a *API) viewStory(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "story-view", userID, storyViewUserLimit, storyViewIPLimit) {
		return
	}
	var req storyViewRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid JSON payload"})
		return
	}
	if len([]rune(req.Reaction)) > 64 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "reaction must be at most 64 characters"})
		return
	}
	storyID := strings.TrimSpace(ctx.UserValue("storyID").(string))
	reaction := strings.TrimSpace(req.Reaction)
	idempotencyKey, err := parseStoryIdempotencyKey(string(ctx.Request.Header.Peek("Idempotency-Key")))
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid Idempotency-Key"})
		return
	}
	requestHash := ""
	if idempotencyKey != "" {
		requestHash, err = hashStoryViewRequest(storyID, reaction)
		if err != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not fingerprint story view"})
			return
		}
	}
	v, replayed, err := a.stories.ViewIdempotent(context.Background(), userID, storyID, reaction, idempotencyKey, requestHash)
	if errors.Is(err, stories.ErrForbidden) {
		writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "story is not available"})
		return
	}
	if errors.Is(err, stories.ErrIdempotencyConflict) {
		writeJSON(ctx, http.StatusConflict, map[string]string{"error": "Idempotency-Key was already used for a different story view"})
		return
	}
	if errors.Is(err, stories.ErrIdempotencyGone) {
		writeJSON(ctx, http.StatusGone, map[string]string{"error": "idempotent story view result is no longer available"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not record view"})
		return
	}
	if replayed {
		ctx.Response.Header.Set("Idempotency-Replayed", "true")
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"view": v})
}

func (a *API) listStoryViews(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "story-views-list", userID, storyViewsUserLimit, storyViewsIPLimit) {
		return
	}
	views, err := a.stories.Views(context.Background(), userID, strings.TrimSpace(ctx.UserValue("storyID").(string)))
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load story views"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"views": views})
}

type storySyncMessage struct {
	Type         string           `json:"type"`
	GlobalCursor int64            `json:"global_cursor"`
	UserCursors  map[string]int64 `json:"user_cursors"`
	Limit        int              `json:"limit"`
}

func (a *API) storyWebSocket(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	origin := strings.TrimSpace(string(ctx.Request.Header.Peek("Origin")))
	if !validStoryOrigin(origin, string(ctx.Host())) {
		writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
		return
	}
	if !a.enforceRate(ctx, "story-ws-connect", userID, storyWSUserLimit, storyWSIPLimit) {
		return
	}
	upgrader := websocket.FastHTTPUpgrader{HandshakeTimeout: 10 * time.Second, CheckOrigin: func(c *fasthttp.RequestCtx) bool {
		return validStoryOrigin(strings.TrimSpace(string(c.Request.Header.Peek("Origin"))), string(c.Host()))
	}}
	if err := upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
		defer conn.Close()
		subscription := &storySubscription{userID: userID, conn: conn}
		if !a.addStorySubscription(subscription) {
			_ = subscription.send(map[string]any{"type": "error", "code": "too_many_connections"})
			return
		}
		defer a.removeStorySubscription(subscription)
		conn.SetReadLimit(64 << 10)
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg storySyncMessage
			dec := json.NewDecoder(bytes.NewReader(payload))
			dec.DisallowUnknownFields()
			if err = dec.Decode(&msg); err != nil {
				_ = subscription.send(map[string]any{"type": "error", "code": "invalid_request"})
				continue
			}
			if len(msg.UserCursors) > 100 {
				_ = subscription.send(map[string]any{"type": "error", "code": "too_many_cursors"})
				continue
			}
			switch msg.Type {
			case "get", "sync":
				allowed, rateErr := a.allowRateBounded("story-signed-url", userID, requestIP(ctx), signedURLUserLimit, signedURLIPLimit)
				if rateErr != nil || !allowed {
					code := "rate_limited"
					if rateErr != nil {
						code = "rate_limit_unavailable"
					}
					_ = subscription.send(map[string]any{"type": "error", "code": code})
					return
				}
				result, e := a.stories.Sync(context.Background(), userID, msg.GlobalCursor, msg.UserCursors, msg.Limit)
				if e != nil {
					log.Printf("story sync failed for user %s: %v", userID, e)
					code := "sync_failed"
					if errors.Is(e, stories.ErrCursorRegression) {
						code = "cursor_regression"
					}
					_ = subscription.send(map[string]any{"type": "error", "code": code})
					continue
				}
				if e = a.signStoryEvents(context.Background(), result.Events); e != nil {
					log.Printf("story media signing failed for user %s: %v", userID, e)
					_ = subscription.send(map[string]any{"type": "error", "code": "media_unavailable"})
					continue
				}
				_ = subscription.send(map[string]any{"type": "stories.batch", "events": result.Events, "global_cursor": result.GlobalCursor, "user_cursors": result.UserCursors, "has_more": result.HasMore})
			case "ack":
				allowed, rateErr := a.allowRateBounded("story-ack", userID, requestIP(ctx), storyAckUserLimit, storyAckIPLimit)
				if rateErr != nil || !allowed {
					code := "rate_limited"
					if rateErr != nil {
						code = "rate_limit_unavailable"
					}
					_ = subscription.send(map[string]any{"type": "error", "code": code})
					if rateErr != nil {
						return
					}
					continue
				}
				if e := a.stories.Ack(context.Background(), userID, msg.GlobalCursor, msg.UserCursors); e != nil {
					log.Printf("story ack failed for user %s: %v", userID, e)
					code := "ack_failed"
					if errors.Is(e, stories.ErrCursorRegression) {
						code = "cursor_regression"
					}
					_ = subscription.send(map[string]any{"type": "error", "code": code})
					continue
				}
				_ = subscription.send(map[string]any{"type": "stories.ack", "global_cursor": msg.GlobalCursor, "user_cursors": msg.UserCursors})
			default:
				_ = subscription.send(map[string]any{"type": "error", "code": "invalid_type"})
			}
		}
	}); err != nil {
		return
	}
}

func validStoryOrigin(origin, requestHost string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Host == "" || u.Host != requestHost || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return u.Path == "" || u.Path == "/"
}

func (a *API) RunStoryExpiry(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired, err := a.stories.Expire(ctx)
			if err != nil {
				log.Printf("story expiry sweep failed: %v", err)
				continue
			}
			for _, story := range expired {
				a.deleteStoryMedia(ctx, story.ID, story.StorageKey)
			}
			pending, err := a.stories.PendingMediaCleanup(ctx, 100)
			if err != nil {
				log.Printf("story media cleanup lookup failed: %v", err)
				continue
			}
			for _, story := range pending {
				a.deleteStoryMedia(ctx, story.ID, story.StorageKey)
			}
			if removed, err := a.stories.CleanupDeleted(ctx, time.Now().Add(-24*time.Hour), 500); err != nil {
				log.Printf("story row cleanup failed: %v", err)
			} else if removed > 0 {
				log.Printf("removed %d story rows older than 24 hours", removed)
			}
			if removed, err := a.stories.CleanupExpiredIdempotency(ctx, 500); err != nil {
				log.Printf("story idempotency cleanup failed: %v", err)
			} else if removed > 0 {
				log.Printf("removed %d expired story idempotency records", removed)
			}
		}
	}
}

func (a *API) deleteStoryMedia(ctx context.Context, storyID, storageKey string) {
	if strings.TrimSpace(storageKey) == "" {
		_ = a.stories.MarkMediaDeleted(ctx, storyID)
		return
	}
	a.evictStoryMediaURL(storageKey)
	if err := a.objects.Delete(ctx, storageKey); err != nil {
		log.Printf("story media delete failed for %s: %v", storyID, err)
		_ = a.stories.RecordMediaCleanupError(ctx, storyID)
		return
	}
	if err := a.stories.MarkMediaDeleted(ctx, storyID); err != nil {
		log.Printf("story media cleanup marker failed for %s: %v", storyID, err)
		_ = a.stories.RecordMediaCleanupError(ctx, storyID)
	}
}
