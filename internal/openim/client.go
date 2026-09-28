// Package openim is the application boundary around OpenIM's Platform API.
// Keep OpenIM-specific JSON and transport details here; services depend only on
// Client and these request/response structs.
package openim

import "context"

type Client interface {
	ProvisionUser(context.Context, *ProvisionUserRequest) (*ProvisionUserResponse, error)
	UpdateProfile(context.Context, *UpdateProfileRequest) error
	ListSessions(context.Context, *ListSessionsRequest) ([]*Session, error)
	RevokeSessions(context.Context, *RevokeSessionsRequest) error
	DiscoverUsers(context.Context, *DiscoverUsersRequest) (*DiscoverUsersResponse, error)
}

type ProvisionUserRequest struct {
	UserID   string `json:"userID"`
	Nickname string `json:"nickname"`
	FaceURL  string `json:"faceURL,omitempty"`
}
type ProvisionUserResponse struct {
	UserID string `json:"userID"`
}
type UpdateProfileRequest struct {
	UserID            string  `json:"userID"`
	Nickname, FaceURL *string `json:"-"`
}
type ListSessionsRequest struct{ UserID string }
type Session struct {
	SessionID, PlatformID, DeviceName, DeviceID string
	LastActiveAt                                int64
}
type RevokeSessionsRequest struct {
	UserID     string
	SessionIDs []string
	AllExcept  string
}
type DiscoverUsersRequest struct{ UserIDs []string }
type DiscoverUsersResponse struct{ UserIDs []string }
