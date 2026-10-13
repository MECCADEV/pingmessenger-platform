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
	r.POST("/v1/stories/{storyID}/view", a.viewStory)
	r.GET("/v1/stories/{storyID}/views", a.listStoryViews)
	r.GET("/v1/stories/ws", a.storyWebSocket)
}

func (a *API) createStory(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	form, err := ctx.Request.MultipartForm()
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "multipart/form-data is required"})
		return
	}
	defer ctx.Request.RemoveMultipartFormFiles()
	file, err := ctx.FormFile("media")
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "media is required"})
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(file.Header.Get("Content-Type")))
	mediaType, validMediaType := storyMediaType(contentType)
	if !validMediaType {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "media must be an image or video"})
		return
	}
	stream, err := file.Open()
	if err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "could not read media"})
		return
	}
	defer stream.Close()
	data, err := storage.ReadLimited(stream, storyMaxBytes)
	if err != nil {
		writeJSON(ctx, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
		return
	}
	caption := strings.TrimSpace(firstForm(form.Value, "caption"))
	if len(caption) > 4096 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "caption must be at most 4096 characters"})
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
	friendSet := make(map[string]struct{}, len(friends))
	for _, id := range friends {
		friendSet[id] = struct{}{}
	}
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
	randomID := make([]byte, 16)
	if _, err = rand.Read(randomID); err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not create story"})
		return
	}
	key := path.Join("stories", userID, hex.EncodeToString(randomID))
	if err = a.objects.Put(context.Background(), key, contentType, data); err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not store story media"})
		return
	}
	story, err := a.stories.Create(context.Background(), stories.CreateParams{OwnerID: userID, MediaType: string(mediaType), StorageKey: key, MediaURL: storage.PublicPath(a.assetBase, key), ContentType: contentType, ByteSize: int64(len(data)), Caption: caption, Visibility: visibility, ExpiresAt: time.Now().Add(expires), Recipients: recipients, VisibilityUsers: ids})
	if err != nil {
		_ = a.objects.Delete(context.Background(), key)
		if errors.Is(err, stories.ErrActiveLimit) {
			writeJSON(ctx, http.StatusTooManyRequests, map[string]string{"error": "maximum of 10 active stories per user reached"})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not persist story"})
		return
	}
	writeJSON(ctx, http.StatusCreated, map[string]any{"story": story})
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

// storyFriends keeps the hot path in memory while retaining a lazy OpenIM
// snapshot fallback after a platform restart.
func (a *API) storyFriends(ctx context.Context, platformID, openIMID string) ([]string, error) {
	a.storyGraphMu.RLock()
	set, cached := a.storyGraph[platformID]
	if cached {
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		a.storyGraphMu.RUnlock()
		return ids, nil
	}
	a.storyGraphMu.RUnlock()
	openIDs, err := a.openIM.ListConversationUserIDs(ctx, &openim.ListConversationUserIDsRequest{UserID: openIMID})
	if err != nil {
		return nil, err
	}
	mapped, err := a.users.PlatformUserIDsByOpenIM(ctx, openIDs)
	if err != nil {
		return nil, err
	}
	set = make(map[string]struct{}, len(mapped))
	for _, id := range mapped {
		set[id] = struct{}{}
	}
	a.storyGraphMu.Lock()
	a.storyGraph[platformID] = set
	a.storyGraphMu.Unlock()
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids, nil
}

// HandleOpenIMWebhook is the platform-side callback boundary used by the
// optional OpenIM webhook gRPC transport. Payloads remain the existing JSON
// callback DTOs, so HTTP and gRPC modes have identical semantics.
func (a *API) HandleOpenIMWebhook(ctx context.Context, command string, payload []byte) ([]byte, error) {
	var event struct {
		OwnerUserID    string   `json:"ownerUserID"`
		OwnerUserIDAlt string   `json:"ownerUserId"`
		FriendUserID   string   `json:"friendUserID"`
		FromUserID     string   `json:"fromUserID"`
		ToUserID       string   `json:"toUserID"`
		FriendUserIDs  []string `json:"friendUserIDs"`
		UserID         string   `json:"userId"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, err
	}
	add := func(owner, friend string) {
		if owner == "" || friend == "" {
			return
		}
		ownerID, err := a.users.PlatformUserIDByOpenIM(ctx, owner)
		if err != nil {
			return
		}
		friendID, err := a.users.PlatformUserIDByOpenIM(ctx, friend)
		if err != nil {
			return
		}
		a.storyGraphMu.Lock()
		if a.storyGraph[ownerID] == nil {
			a.storyGraph[ownerID] = map[string]struct{}{}
		}
		a.storyGraph[ownerID][friendID] = struct{}{}
		a.storyGraphMu.Unlock()
	}
	remove := func(owner, friend string) {
		if owner == "" || friend == "" {
			return
		}
		ownerID, err := a.users.PlatformUserIDByOpenIM(ctx, owner)
		if err != nil {
			return
		}
		friendID, err := a.users.PlatformUserIDByOpenIM(ctx, friend)
		if err != nil {
			return
		}
		a.storyGraphMu.Lock()
		if a.storyGraph[ownerID] != nil {
			delete(a.storyGraph[ownerID], friendID)
		}
		a.storyGraphMu.Unlock()
	}
	switch command {
	case "callbackAfterAddFriendCommand", "callbackAfterAddFriendAgreeCommand":
		add(event.FromUserID, event.ToUserID)
		add(event.ToUserID, event.FromUserID)
	case "callbackAfterDeleteFriendCommand":
		remove(event.OwnerUserID, event.FriendUserID)
		remove(event.FriendUserID, event.OwnerUserID)
	case "callbackAfterImportFriendsCommand":
		for _, friend := range event.FriendUserIDs {
			add(event.OwnerUserID, friend)
			add(friend, event.OwnerUserID)
		}
	case "callbackAfterCreateSingleChatConversationsCommand":
		owner := event.OwnerUserID
		if owner == "" {
			owner = event.OwnerUserIDAlt
		}
		add(owner, event.UserID)
		add(event.UserID, owner)
	}
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
	v, err := a.stories.View(context.Background(), userID, strings.TrimSpace(ctx.UserValue("storyID").(string)), strings.TrimSpace(req.Reaction))
	if errors.Is(err, stories.ErrForbidden) {
		writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "story is not available"})
		return
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not record view"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"view": v})
}

func (a *API) listStoryViews(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
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
	upgrader := websocket.FastHTTPUpgrader{HandshakeTimeout: 10 * time.Second, CheckOrigin: func(c *fasthttp.RequestCtx) bool {
		origin := strings.TrimSpace(string(c.Request.Header.Peek("Origin")))
		return origin == "" || strings.Contains(origin, string(c.Host()))
	}}
	if err := upgrader.Upgrade(ctx, func(conn *websocket.Conn) {
		defer conn.Close()
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
				_ = conn.WriteJSON(map[string]any{"type": "error", "code": "invalid_request"})
				continue
			}
			switch msg.Type {
			case "sync":
				result, e := a.stories.Sync(context.Background(), userID, msg.GlobalCursor, msg.UserCursors, msg.Limit)
				if e != nil {
					log.Printf("story sync failed for user %s: %v", userID, e)
					code := "sync_failed"
					if errors.Is(e, stories.ErrCursorRegression) {
						code = "cursor_regression"
					}
					_ = conn.WriteJSON(map[string]any{"type": "error", "code": code})
					continue
				}
				_ = conn.WriteJSON(map[string]any{"type": "stories.batch", "events": result.Events, "global_cursor": result.GlobalCursor, "user_cursors": result.UserCursors, "has_more": result.HasMore})
			case "ack":
				if e := a.stories.Ack(context.Background(), userID, msg.GlobalCursor, msg.UserCursors); e != nil {
					log.Printf("story ack failed for user %s: %v", userID, e)
					code := "ack_failed"
					if errors.Is(e, stories.ErrCursorRegression) {
						code = "cursor_regression"
					}
					_ = conn.WriteJSON(map[string]any{"type": "error", "code": code})
					continue
				}
				_ = conn.WriteJSON(map[string]any{"type": "stories.ack", "global_cursor": msg.GlobalCursor, "user_cursors": msg.UserCursors})
			default:
				_ = conn.WriteJSON(map[string]any{"type": "error", "code": "invalid_type"})
			}
		}
	}); err != nil {
		return
	}
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
		}
	}
}

func (a *API) deleteStoryMedia(ctx context.Context, storyID, storageKey string) {
	if err := a.objects.Delete(ctx, storageKey); err != nil {
		log.Printf("story media delete failed for %s: %v", storyID, err)
		return
	}
	if err := a.stories.MarkMediaDeleted(ctx, storyID); err != nil {
		log.Printf("story media cleanup marker failed for %s: %v", storyID, err)
	}
}
