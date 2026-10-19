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
	ListFriendIDs(context.Context, *ListFriendIDsRequest) ([]string, error)
	ListBlocks(context.Context, *ListBlocksRequest) ([]*BlockInfo, error)
	AddBlock(context.Context, *BlockRequest) error
	RemoveBlock(context.Context, *BlockRequest) error
	DeleteUser(context.Context, *DeleteUserRequest) error
	ListConversationUserIDs(context.Context, *ListConversationUserIDsRequest) ([]string, error)
	GetUserToken(context.Context, *GetUserTokenRequest) (*GetUserTokenResponse, error)
	KickTokens(context.Context, []string) error
	SendCustomMessage(context.Context, *SendCustomMessageRequest) (*SendCustomMessageResponse, error)
	ModifyMessage(context.Context, *ModifyMessageRequest) error
	RevokeMessage(context.Context, *RevokeMessageRequest) error
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
type ListFriendIDsRequest struct {
	UserID string `json:"userID"`
}
type ListBlocksRequest struct {
	UserID string `json:"userID"`
}
type BlockRequest struct {
	OwnerUserID string `json:"ownerUserID"`
	BlockUserID string `json:"blackUserID"`
}
type BlockInfo struct {
	UserID    string `json:"userID"`
	CreatedAt int64  `json:"createTime"`
}
type DeleteUserRequest struct {
	UserID string `json:"userID"`
}
type ListConversationUserIDsRequest struct {
	UserID string `json:"ownerUserID"`
}
type GetUserTokenRequest struct {
	UserID     string `json:"userID"`
	PlatformID int32  `json:"platformID"`
}
type GetUserTokenResponse struct {
	Token             string `json:"token"`
	ExpireTimeSeconds int64  `json:"expireTimeSeconds"`
}

type SendCustomMessageRequest struct {
	SendID          string
	RecvID          string
	Content         map[string]any
	SenderNickname  string
	SessionType     int32
	ContentType     int32
	NotOfflinePush  bool
	OfflinePushInfo map[string]any
	Ex              string
}

type SendCustomMessageResponse struct {
	ServerMsgID string `json:"serverMsgID"`
	ClientMsgID string `json:"clientMsgID"`
	SendTime    int64  `json:"sendTime"`
}

type ModifyMessageRequest struct {
	ConversationID string `json:"conversationID"`
	Seq            int64  `json:"seq"`
	UserID         string `json:"userID,omitempty"`
	OldContent     string `json:"oldContent"`
	NewContent     string `json:"newContent"`
}

type RevokeMessageRequest struct {
	ConversationID string `json:"conversationID"`
	Seq            int64  `json:"seq"`
	UserID         string `json:"userID"`
}
