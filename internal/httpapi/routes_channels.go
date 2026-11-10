package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/channels"
	"pingmessenger/internal/openim"
)

const (
	channelIDPrefix           = "ch_"
	openIMGroupAdmin          = int32(60)
	openIMGroupOwner          = int32(100)
	channelSearchLimitDefault = 20
	channelPinLimit           = 100
)

type createChannelInput struct {
	Name        string `json:"name" validate:"required,min=1,max=120"`
	Description string `json:"description,omitempty" validate:"omitempty,max=2000"`
	PhotoURL    string `json:"photo_url,omitempty" validate:"omitempty,max=2048"`
	Visibility  string `json:"visibility" validate:"required,oneof=public private"`
}

type pinChannelPostInput struct {
	MessageID string `json:"message_id" validate:"required,min=1,max=128"`
}

func (a *API) registerChannelRoutes(r *router.Router) {
	r.POST("/v1/channels", a.createChannel)
	r.GET("/v1/channels/search", a.searchChannels)
	r.GET("/v1/channels/{channelID}", a.getChannel)
	r.POST("/v1/channels/{channelID}/pins", a.pinChannelPost)
	r.DELETE("/v1/channels/{channelID}/pins/{messageID}", a.unpinChannelPost)
	r.GET("/v1/channels/{channelID}/pins", a.listChannelPins)
}

func (a *API) createChannel(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "channel-create", userID, 10, 30) {
		return
	}
	var req createChannelInput
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.PhotoURL = strings.TrimSpace(req.PhotoURL)
	if req.Name == "" {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "name is required"})
		return
	}
	if req.PhotoURL != "" {
		photoURL, err := url.ParseRequestURI(req.PhotoURL)
		if err != nil || photoURL.Scheme != "https" || photoURL.Host == "" {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "photo_url must be an https URL"})
			return
		}
	}
	ownerOpenIMID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil || ownerOpenIMID == "" {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM identity is unavailable"})
		return
	}
	channelUUID := uuid.NewString()
	groupID := channelIDPrefix + strings.ReplaceAll(channelUUID, "-", "")
	needVerification := channelVisibilityJoinPolicy(channels.Visibility(req.Visibility))
	group, err := a.openIM.CreateGroup(context.Background(), &openim.CreateGroupRequest{
		OwnerUserID: ownerOpenIMID, GroupID: groupID, GroupName: req.Name,
		Introduction: req.Description, FaceURL: req.PhotoURL, NeedVerification: needVerification,
	})
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not create channel group"})
		return
	}
	if group.GroupID != groupID || !strings.HasPrefix(group.GroupID, channelIDPrefix) {
		_ = a.openIM.DismissGroup(context.Background(), group.GroupID)
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "OpenIM returned an invalid channel group ID"})
		return
	}
	channel, err := a.channels.Create(context.Background(), channels.CreateParams{
		OpenIMGroupID: groupID, CreatedBy: userID, Name: req.Name, Description: req.Description,
		PhotoURL: req.PhotoURL, Visibility: channels.Visibility(req.Visibility),
	})
	if err != nil {
		if cleanupErr := a.openIM.DismissGroup(context.Background(), groupID); cleanupErr != nil {
			writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not save channel or clean up its OpenIM group"})
			return
		}
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not save channel"})
		return
	}
	channel.MemberCount = 1
	writeJSON(ctx, http.StatusCreated, map[string]any{"channel": channel})
}

func (a *API) searchChannels(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "channel-search", userID, 60, 120) {
		return
	}
	query := strings.TrimSpace(string(ctx.QueryArgs().Peek("q")))
	if len(query) > 120 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "q must be at most 120 characters"})
		return
	}
	limit, offset := channelSearchLimitDefault, 0
	if raw := string(ctx.QueryArgs().Peek("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid limit"})
			return
		}
		limit = parsed
	}
	if raw := string(ctx.QueryArgs().Peek("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid offset"})
			return
		}
		offset = parsed
	}
	if err := channels.ValidateSearchPage(limit, offset); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	results, total, err := a.channels.SearchPublic(context.Background(), query, limit, offset)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not search channels"})
		return
	}
	if len(results) > 0 {
		groupIDs := make([]string, 0, len(results))
		for _, channel := range results {
			groupIDs = append(groupIDs, channel.OpenIMGroupID)
		}
		groupInfos, groupErr := a.openIM.GetGroupsInfo(context.Background(), groupIDs)
		if groupErr != nil {
			writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not load channel member counts"})
			return
		}
		infos := make(map[string]*openim.GroupInfo, len(groupInfos))
		for _, groupInfo := range groupInfos {
			if groupInfo != nil {
				infos[groupInfo.GroupID] = groupInfo
			}
		}
		for _, channel := range results {
			info := infos[channel.OpenIMGroupID]
			if info == nil {
				writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "channel group is unavailable"})
				return
			}
			channel.MemberCount = info.MemberCount
		}
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"channels": results, "total": total, "limit": limit, "offset": offset})
}

func (a *API) getChannel(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "channel-detail", userID, 120, 300) {
		return
	}
	channel, found := a.channelByRequestID(ctx)
	if !found {
		return
	}
	member, role, err := a.channelMembership(channel, userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not verify channel membership"})
		return
	}
	if channel.Visibility == channels.VisibilityPrivate && !member {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	info, err := a.channelOpenIMInfo(channel.OpenIMGroupID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not load channel info"})
		return
	}
	channel.MemberCount = info.MemberCount
	writeJSON(ctx, http.StatusOK, map[string]any{"channel": channel, "is_member": member, "is_admin": member && isChannelAdminRole(role)})
}

func (a *API) pinChannelPost(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "channel-pin", userID, 60, 120) {
		return
	}
	channel, found := a.channelByRequestID(ctx)
	if !found {
		return
	}
	if !a.requireChannelAdmin(ctx, channel, userID) {
		return
	}
	var req pinChannelPostInput
	if err := a.DecodeAndValidate(ctx, &req); err != nil {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	req.MessageID = strings.TrimSpace(req.MessageID)
	if req.MessageID == "" {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "message_id is required"})
		return
	}
	pin, created, err := a.channels.Pin(context.Background(), channel.ID, req.MessageID, userID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not pin channel post"})
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(ctx, status, map[string]any{"pin": pin})
}

func (a *API) unpinChannelPost(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "channel-pin", userID, 60, 120) {
		return
	}
	channel, found := a.channelByRequestID(ctx)
	if !found {
		return
	}
	if !a.requireChannelAdmin(ctx, channel, userID) {
		return
	}
	messageID := strings.TrimSpace(ctx.UserValue("messageID").(string))
	if len(messageID) == 0 || len(messageID) > 128 {
		writeJSON(ctx, http.StatusUnprocessableEntity, map[string]string{"error": "invalid messageID"})
		return
	}
	removed, err := a.channels.Unpin(context.Background(), channel.ID, messageID)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not unpin channel post"})
		return
	}
	if !removed {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "pinned post not found"})
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

func (a *API) listChannelPins(ctx *fasthttp.RequestCtx) {
	userID, _, ok := a.authenticated(ctx)
	if !ok {
		writeJSON(ctx, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !a.enforceRate(ctx, "channel-pin-list", userID, 60, 120) {
		return
	}
	channel, found := a.channelByRequestID(ctx)
	if !found {
		return
	}
	member, _, err := a.channelMembership(channel, userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not verify channel membership"})
		return
	}
	if !member {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	pins, err := a.channels.Pins(context.Background(), channel.ID, channelPinLimit)
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load pinned posts"})
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"pins": pins})
}

func (a *API) channelByRequestID(ctx *fasthttp.RequestCtx) (*channels.Channel, bool) {
	id := strings.TrimSpace(ctx.UserValue("channelID").(string))
	if _, err := uuid.Parse(id); err != nil {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return nil, false
	}
	channel, err := a.channels.ByID(context.Background(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(ctx, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return nil, false
	}
	if err != nil {
		writeJSON(ctx, http.StatusInternalServerError, map[string]string{"error": "could not load channel"})
		return nil, false
	}
	return channel, true
}

func (a *API) channelMembership(channel *channels.Channel, userID string) (bool, int32, error) {
	openIMID, err := a.users.OpenIMUserID(context.Background(), userID)
	if err != nil || openIMID == "" {
		return false, 0, fmt.Errorf("OpenIM identity is unavailable")
	}
	role, member, err := a.openIM.GetGroupMemberRole(context.Background(), channel.OpenIMGroupID, openIMID)
	return member, role, err
}

func (a *API) channelOpenIMInfo(groupID string) (*openim.GroupInfo, error) {
	infos, err := a.openIM.GetGroupsInfo(context.Background(), []string{groupID})
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if info != nil && info.GroupID == groupID {
			return info, nil
		}
	}
	return nil, fmt.Errorf("OpenIM group not found")
}

func (a *API) requireChannelAdmin(ctx *fasthttp.RequestCtx, channel *channels.Channel, userID string) bool {
	member, role, err := a.channelMembership(channel, userID)
	if err != nil {
		writeJSON(ctx, http.StatusBadGateway, map[string]string{"error": "could not verify channel permissions"})
		return false
	}
	if !member || !isChannelAdminRole(role) {
		writeJSON(ctx, http.StatusForbidden, map[string]string{"error": "channel owner or admin required"})
		return false
	}
	return true
}

func isChannelAdminRole(role int32) bool { return role == openIMGroupAdmin || role == openIMGroupOwner }

func channelVisibilityJoinPolicy(visibility channels.Visibility) int32 {
	if visibility == channels.VisibilityPublic {
		return 2
	}
	return 1
}
