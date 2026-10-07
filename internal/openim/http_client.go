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
)

// HTTPClient owns reusable, concurrency-safe HTTP connections. Construct it
// once at startup and inject Client into services; never make one per request.
type HTTPClient struct {
	baseURL      *url.URL
	token        string
	sharedSecret string
	adminUserID  string
	tokenExpiry  time.Time
	mu           sync.Mutex
	http         *http.Client
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

func NewHTTPClient(baseURL, token string) (*HTTPClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid OpenIM API URL")
	}
	return &HTTPClient{baseURL: u, token: token, http: &http.Client{Timeout: 10 * time.Second}}, nil
}
func (c *HTTPClient) Close() { c.http.CloseIdleConnections() }

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

func (c *HTTPClient) call(ctx context.Context, operation string, request, response any) error {
	if err := c.ensureAdminToken(ctx); err != nil {
		return err
	}
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
	if c.token != "" {
		// OpenIM's Gin middleware reads the lower-case platform header named
		// "token"; it does not use HTTP Authorization/Bearer.
		httpRequest.Header.Set("token", c.token)
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
