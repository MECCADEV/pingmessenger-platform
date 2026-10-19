package openim

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	json "github.com/goccy/go-json"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"pingmessenger/internal/openim/conversationpb"
)

// HTTPClient owns reusable, concurrency-safe HTTP connections. Construct it
// once at startup and inject Client into services; never make one per request.
type HTTPClient struct {
	baseURL          *url.URL
	token            string
	sharedSecret     string
	adminUserID      string
	tokenExpiry      time.Time
	mu               sync.Mutex
	http             *http.Client
	conversation     conversationpb.ConversationClient
	conversationConn *grpc.ClientConn
}

// NewHTTPClientWithSharedSecret obtains a signed OpenIM admin token with the
// shared secret and refreshes it before expiry. The shared secret is never
// sent to ordinary Platform API endpoints.
func NewHTTPClientWithSharedSecret(baseURL, token, sharedSecret, adminUserID string) (*HTTPClient, error) {
	c, err := NewHTTPClient(baseURL, token)
	if err != nil {
		return nil, err
	}
	c.sharedSecret, c.adminUserID = sharedSecret, adminUserID
	return c, nil
}

// NewHTTPClientWithSharedSecretAndConversationGRPC keeps the Platform's
// existing HTTP OpenIM boundary for user/auth operations, while resolving the
// story recipient graph through OpenIM's native conversation gRPC service.
func NewHTTPClientWithSharedSecretAndConversationGRPC(baseURL, token, sharedSecret, adminUserID, conversationTarget string) (*HTTPClient, error) {
	c, err := NewHTTPClientWithSharedSecret(baseURL, token, sharedSecret, adminUserID)
	if err != nil {
		return nil, err
	}
	if conversationTarget == "" {
		return c, nil
	}
	conn, err := grpc.Dial(conversationTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial OpenIM conversation gRPC: %w", err)
	}
	c.conversationConn = conn
	c.conversation = conversationpb.NewConversationClient(conn)
	return c, nil
}

func NewHTTPClient(baseURL, token string) (*HTTPClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid OpenIM API URL")
	}
	return &HTTPClient{baseURL: u, token: token, http: &http.Client{Timeout: 10 * time.Second}}, nil
}
func (c *HTTPClient) Close() {
	c.http.CloseIdleConnections()
	if c.conversationConn != nil {
		_ = c.conversationConn.Close()
	}
}

func (c *HTTPClient) ProvisionUser(ctx context.Context, request *ProvisionUserRequest) (*ProvisionUserResponse, error) {
	response := new(ProvisionUserResponse)
	if err := c.call(ctx, "user/user_register", struct {
		Users []*ProvisionUserRequest `json:"users"`
	}{Users: []*ProvisionUserRequest{request}}, response); err != nil {
		return nil, err
	}
	return response, nil
}
func (c *HTTPClient) UpdateProfile(ctx context.Context, request *UpdateProfileRequest) error {
	return c.call(ctx, "user/update_user_info_ex", struct {
		UserInfo struct {
			UserID   string  `json:"userID"`
			Nickname *string `json:"nickname,omitempty"`
			FaceURL  *string `json:"faceURL,omitempty"`
		} `json:"userInfo"`
	}{UserInfo: struct {
		UserID   string  `json:"userID"`
		Nickname *string `json:"nickname,omitempty"`
		FaceURL  *string `json:"faceURL,omitempty"`
	}{UserID: request.UserID, Nickname: request.Nickname, FaceURL: request.FaceURL}}, nil)
}
func (c *HTTPClient) ListSessions(ctx context.Context, request *ListSessionsRequest) ([]*Session, error) {
	response := struct {
		Sessions []*Session `json:"sessions"`
	}{}
	if err := c.call(ctx, "auth/sessions/list", request, &response); err != nil {
		return nil, err
	}
	return response.Sessions, nil
}
func (c *HTTPClient) RevokeSessions(ctx context.Context, request *RevokeSessionsRequest) error {
	return c.call(ctx, "auth/sessions/revoke", request, nil)
}
func (c *HTTPClient) DiscoverUsers(ctx context.Context, request *DiscoverUsersRequest) (*DiscoverUsersResponse, error) {
	response := new(DiscoverUsersResponse)
	if err := c.call(ctx, "user/discover", request, response); err != nil {
		return nil, err
	}
	return response, nil
}
func (c *HTTPClient) ListFriendIDs(ctx context.Context, request *ListFriendIDsRequest) ([]string, error) {
	response := struct {
		FriendIDs []string `json:"friendIDs"`
	}{}
	if err := c.call(ctx, "friend/get_friend_id", request, &response); err != nil {
		return nil, err
	}
	return response.FriendIDs, nil
}
func (c *HTTPClient) ListBlocks(ctx context.Context, request *ListBlocksRequest) ([]*BlockInfo, error) {
	response := struct {
		Blacks []struct {
			BlackUserInfo struct {
				UserID string `json:"userID"`
			} `json:"blackUserInfo"`
			CreateTime int64 `json:"createTime"`
		} `json:"blacks"`
	}{}
	payload := struct {
		UserID     string `json:"userID"`
		Pagination struct {
			PageNumber int32 `json:"pageNumber"`
			ShowNumber int32 `json:"showNumber"`
		} `json:"pagination"`
	}{UserID: request.UserID}
	payload.Pagination.PageNumber = 1
	payload.Pagination.ShowNumber = 100
	if err := c.call(ctx, "friend/get_black_list", payload, &response); err != nil {
		return nil, err
	}
	blocks := make([]*BlockInfo, 0, len(response.Blacks))
	for _, black := range response.Blacks {
		if black.BlackUserInfo.UserID != "" {
			blocks = append(blocks, &BlockInfo{UserID: black.BlackUserInfo.UserID, CreatedAt: black.CreateTime})
		}
	}
	return blocks, nil
}
func (c *HTTPClient) AddBlock(ctx context.Context, request *BlockRequest) error {
	return c.call(ctx, "friend/add_black", request, nil)
}
func (c *HTTPClient) RemoveBlock(ctx context.Context, request *BlockRequest) error {
	return c.call(ctx, "friend/remove_black", request, nil)
}
func (c *HTTPClient) DeleteUser(ctx context.Context, request *DeleteUserRequest) error {
	return c.call(ctx, "user/delete_users", struct {
		UserIDs []string `json:"userIDs"`
	}{UserIDs: []string{request.UserID}}, nil)
}
func (c *HTTPClient) ListConversationUserIDs(ctx context.Context, request *ListConversationUserIDsRequest) ([]string, error) {
	if c.conversation != nil {
		operationID := make([]byte, 12)
		if _, err := rand.Read(operationID); err != nil {
			return nil, fmt.Errorf("create OpenIM operation ID: %w", err)
		}
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("operationID", hex.EncodeToString(operationID), "opUserID", c.adminUserID, "platform", "platform"))
		response, err := c.conversation.GetAllConversations(ctx, &conversationpb.GetAllConversationsReq{OwnerUserID: request.UserID})
		if err != nil {
			return nil, fmt.Errorf("call OpenIM conversation gRPC: %w", err)
		}
		ids := make([]string, 0, len(response.GetConversations()))
		for _, item := range response.GetConversations() {
			if item.GetConversationType() == 1 && item.GetUserID() != "" {
				ids = append(ids, item.GetUserID())
			}
		}
		return ids, nil
	}
	response := struct {
		Conversations []struct {
			UserID           string `json:"userID"`
			ConversationType int32  `json:"conversationType"`
		} `json:"conversations"`
	}{}
	if err := c.call(ctx, "conversation/get_all_conversations", request, &response); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(response.Conversations))
	for _, conversation := range response.Conversations {
		if conversation.ConversationType == 1 && conversation.UserID != "" {
			ids = append(ids, conversation.UserID)
		}
	}
	return ids, nil
}
func (c *HTTPClient) GetUserToken(ctx context.Context, request *GetUserTokenRequest) (*GetUserTokenResponse, error) {
	response := new(GetUserTokenResponse)
	if err := c.call(ctx, "auth/get_user_token", request, response); err != nil {
		return nil, err
	}
	return response, nil
}
func (c *HTTPClient) KickTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	return c.call(ctx, "auth/kick_tokens", struct {
		Tokens []string `json:"tokens"`
	}{Tokens: tokens}, nil)
}

func (c *HTTPClient) SendCustomMessage(ctx context.Context, request *SendCustomMessageRequest) (*SendCustomMessageResponse, error) {
	response := new(SendCustomMessageResponse)
	payload := struct {
		RecvID          string         `json:"recvID"`
		SendID          string         `json:"sendID"`
		Content         map[string]any `json:"content"`
		ContentType     int32          `json:"contentType"`
		SessionType     int32          `json:"sessionType"`
		SenderNickname  string         `json:"senderNickname,omitempty"`
		NotOfflinePush  bool           `json:"notOfflinePush,omitempty"`
		OfflinePushInfo map[string]any `json:"offlinePushInfo,omitempty"`
		Ex              string         `json:"ex,omitempty"`
	}{RecvID: request.RecvID, SendID: request.SendID, Content: request.Content, ContentType: request.ContentType, SessionType: request.SessionType, SenderNickname: request.SenderNickname, NotOfflinePush: request.NotOfflinePush, OfflinePushInfo: request.OfflinePushInfo, Ex: request.Ex}
	if err := c.call(ctx, "msg/send_msg", payload, response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *HTTPClient) ModifyMessage(ctx context.Context, request *ModifyMessageRequest) error {
	return c.call(ctx, "msg/modify_message", request, nil)
}

func (c *HTTPClient) RevokeMessage(ctx context.Context, request *RevokeMessageRequest) error {
	return c.call(ctx, "msg/revoke_msg", request, nil)
}

func (c *HTTPClient) call(ctx context.Context, operation string, request, response any) error {
	// An OpenIM restart or signing-key rotation can invalidate a cached admin
	// token before its advertised expiry. Retry once with a fresh token. The
	// operations crossing this boundary are idempotent, and successful calls are
	// never retried.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureAdminToken(ctx); err != nil {
			return err
		}
		lastErr = c.callOnce(ctx, operation, request, response)
		if lastErr == nil || c.sharedSecret == "" || attempt == 1 {
			return lastErr
		}
		c.invalidateAdminToken()
	}
	return lastErr
}

func (c *HTTPClient) callOnce(ctx context.Context, operation string, request, response any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal OpenIM request: %w", err)
	}
	u := c.baseURL.JoinPath(strings.Split(operation, "/")...)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OpenIM request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	// OpenIM requires a non-empty operationID for request tracing. Generate a
	// collision-resistant ID per call without coupling this boundary to a web
	// request implementation.
	operationID := make([]byte, 12)
	if _, err := rand.Read(operationID); err != nil {
		return fmt.Errorf("create OpenIM operation ID: %w", err)
	}
	httpRequest.Header.Set("operationID", hex.EncodeToString(operationID))
	if token := c.currentToken(); token != "" {
		// OpenIM's Gin middleware reads the lower-case platform header named
		// "token"; it does not use HTTP Authorization/Bearer.
		httpRequest.Header.Set("token", token)
	}
	httpResponse, err := c.http.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("call OpenIM %s: %w", operation, err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		limited, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4096))
		return fmt.Errorf("OpenIM %s returned %d: %s", operation, httpResponse.StatusCode, strings.TrimSpace(string(limited)))
	}
	var envelope struct {
		ErrCode int             `json:"errCode"`
		ErrMsg  string          `json:"errMsg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(httpResponse.Body).Decode(&envelope); err != nil && err != io.EOF {
		return fmt.Errorf("decode OpenIM %s response: %w", operation, err)
	}
	if envelope.ErrCode != 0 {
		return fmt.Errorf("OpenIM %s failed (%d): %s", operation, envelope.ErrCode, envelope.ErrMsg)
	}
	if response != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, response); err != nil {
			return fmt.Errorf("decode OpenIM %s data: %w", operation, err)
		}
	}
	return nil
}

func (c *HTTPClient) invalidateAdminToken() {
	c.mu.Lock()
	c.token = ""
	c.tokenExpiry = time.Time{}
	c.mu.Unlock()
}

func (c *HTTPClient) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *HTTPClient) ensureAdminToken(ctx context.Context) error {
	if c.sharedSecret == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Add(time.Minute).Before(c.tokenExpiry) {
		return nil
	}
	body, err := json.Marshal(struct {
		Secret string `json:"secret"`
		UserID string `json:"userID"`
	}{Secret: c.sharedSecret, UserID: c.adminUserID})
	if err != nil {
		return fmt.Errorf("marshal OpenIM admin token request: %w", err)
	}
	u := c.baseURL.JoinPath("auth", "get_admin_token")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OpenIM admin token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	operationID := make([]byte, 12)
	if _, err = rand.Read(operationID); err != nil {
		return fmt.Errorf("create OpenIM admin token operation ID: %w", err)
	}
	req.Header.Set("operationID", hex.EncodeToString(operationID))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call OpenIM admin token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("OpenIM admin token returned %d", resp.StatusCode)
	}
	var envelope struct {
		ErrCode int    `json:"errCode"`
		ErrMsg  string `json:"errMsg"`
		Data    struct {
			Token             string `json:"token"`
			ExpireTimeSeconds int64  `json:"expireTimeSeconds"`
		} `json:"data"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode OpenIM admin token response: %w", err)
	}
	if envelope.ErrCode != 0 || envelope.Data.Token == "" {
		return fmt.Errorf("OpenIM admin token failed (%d): %s", envelope.ErrCode, envelope.ErrMsg)
	}
	c.token = envelope.Data.Token
	c.tokenExpiry = time.Now().Add(time.Duration(envelope.Data.ExpireTimeSeconds) * time.Second)
	return nil
}
