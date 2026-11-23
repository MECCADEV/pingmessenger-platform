package httpapi

import (
	"context"
	"errors"

	"pingmessenger/internal/openim"
	"pingmessenger/internal/stories"
)

// storyOwnerBlocked reports whether an owner has blocked the viewer.
func (a *API) storyOwnerBlocked(ctx context.Context, viewerID, ownerID string) (bool, error) {
	if viewerID == ownerID && viewerID != "" {
		return false, nil
	}
	checker, ok := a.openIM.(openim.BlockStatusChecker)
	if !ok || viewerID == "" || ownerID == "" {
		return false, errors.New("OpenIM directional block status is unavailable")
	}
	viewerOpenIMID, err := a.users.OpenIMUserID(ctx, viewerID)
	if err != nil {
		return false, err
	}
	ownerOpenIMID, err := a.users.OpenIMUserID(ctx, ownerID)
	if err != nil {
		return false, err
	}
	return checker.IsBlocked(ctx, ownerOpenIMID, viewerOpenIMID)
}

func (a *API) visibleStoryEvents(ctx context.Context, viewerID string, events []*stories.Event) ([]*stories.Event, error) {
	viewerIdentity, err := a.users.OpenIMUserIDsByPlatform(ctx, []string{viewerID})
	if err != nil {
		return nil, err
	}
	viewerOpenIMID := viewerIdentity[viewerID]
	if viewerOpenIMID == "" {
		// Accounts without an OpenIM identity cannot be checked against the
		// directional block list. Keep their own and deletion events, and fail
		// closed for other owners instead of breaking the story sync stream.
		visible := make([]*stories.Event, 0, len(events))
		for _, event := range events {
			if event != nil && (event.Type == "deleted" || event.OwnerID == viewerID) {
				visible = append(visible, event)
			}
		}
		return visible, nil
	}
	ownerPlatformIDs := make([]string, 0, len(events))
	for _, event := range events {
		if event != nil && event.Type != "deleted" && event.OwnerID != viewerID {
			ownerPlatformIDs = append(ownerPlatformIDs, event.OwnerID)
		}
	}
	ownerOpenIMIDs, err := a.users.OpenIMUserIDsByPlatform(ctx, ownerPlatformIDs)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(ownerOpenIMIDs))
	for _, openIMID := range ownerOpenIMIDs {
		targets = append(targets, openIMID)
	}
	blocked, err := a.blockedByRequester(ctx, viewerOpenIMID, targets)
	if err != nil {
		return nil, err
	}
	visible := make([]*stories.Event, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		if event.Type == "deleted" {
			visible = append(visible, event)
			continue
		}
		ownerOpenIMID, knownOwner := ownerOpenIMIDs[event.OwnerID]
		if event.OwnerID == viewerID || (knownOwner && !blocked[ownerOpenIMID]) {
			visible = append(visible, event)
		}
	}
	return visible, nil
}
