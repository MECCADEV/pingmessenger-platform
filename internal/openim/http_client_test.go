package openim

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"pingmessenger/internal/openim/conversationpb"
)

func TestSharedClientRefreshesRejectedAdminToken(t *testing.T) {
	adminRequests := 0
	operationRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/get_admin_token":
			adminRequests++
			_, _ = w.Write([]byte(`{"errCode":0,"data":{"token":"fresh-admin","expireTimeSeconds":3600}}`))
		case "/auth/kick_tokens":
			operationRequests++
			if r.Header.Get("token") != "fresh-admin" {
				_, _ = w.Write([]byte(`{"errCode":401,"errMsg":"invalid admin token"}`))
				return
			}
			_, _ = w.Write([]byte(`{"errCode":0}`))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClientWithSharedSecret(server.URL, "", "shared-secret", "imAdmin")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a token that was valid before OpenIM restarted. The retry must
	// discard it and obtain a new admin token before kicking the prior token.
	client.token = "stale-admin"
	client.tokenExpiry = time.Now().Add(time.Hour)
	if err := client.KickTokens(context.Background(), []string{"prior-token"}); err != nil {
		t.Fatal(err)
	}
	if adminRequests != 1 || operationRequests != 2 {
		t.Fatalf("admin requests=%d operation requests=%d; want 1 and 2", adminRequests, operationRequests)
	}
}

type conversationGRPCTestServer struct {
	conversationpb.UnimplementedConversationServer
}

func (conversationGRPCTestServer) GetAllConversations(context.Context, *conversationpb.GetAllConversationsReq) (*conversationpb.GetAllConversationsResp, error) {
	return &conversationpb.GetAllConversationsResp{Conversations: []*conversationpb.Conversation{
		{UserID: "u2", ConversationType: 1},
		{UserID: "group-1", ConversationType: 3},
	}}, nil
}

func TestListSessionsUsesSharedClientAndPlatformAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/sessions/list" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("token"); got != "platform-token" {
			t.Fatalf("OpenIM token header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errCode":0,"data":{"sessions":[{"SessionID":"session-1"}]}}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient(server.URL, "platform-token")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := client.ListSessions(context.Background(), &ListSessionsRequest{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "session-1" {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestProvisionAndProfileUsePinnedOpenIMContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/user/user_register":
			if !strings.Contains(string(body), `"users":[{"userID":"u1","nickname":"Ada"`) {
				t.Fatalf("registration body = %s", body)
			}
			_, _ = w.Write([]byte(`{"errCode":0,"data":{}}`))
		case "/user/update_user_info_ex":
			if !strings.Contains(string(body), `"userInfo":{"userID":"u1","faceURL":"https://cdn.example/avatar.png"}`) {
				t.Fatalf("profile body = %s", body)
			}
			_, _ = w.Write([]byte(`{"errCode":0,"data":{}}`))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ProvisionUser(context.Background(), &ProvisionUserRequest{UserID: "u1", Nickname: "Ada"}); err != nil {
		t.Fatal(err)
	}
	faceURL := "https://cdn.example/avatar.png"
	if err = client.UpdateProfile(context.Background(), &UpdateProfileRequest{UserID: "u1", FaceURL: &faceURL}); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestListConversationUserIDsFiltersGroupConversations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversation/get_all_conversations" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"errCode":0,"data":{"conversations":[{"userID":"u2","conversationType":1},{"userID":"g1","conversationType":3}]}}`))
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := client.ListConversationUserIDs(context.Background(), &ListConversationUserIDsRequest{UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "u2" {
		t.Fatalf("ids = %#v", ids)
	}
}

func TestListConversationUserIDsUsesConversationGRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	conversationpb.RegisterConversationServer(server, conversationGRPCTestServer{})
	go server.Serve(listener)
	defer server.Stop()

	client, err := NewHTTPClientWithSharedSecretAndConversationGRPC("http://127.0.0.1:1", "", "", "imAdmin", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ids, err := client.ListConversationUserIDs(context.Background(), &ListConversationUserIDsRequest{UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "u2" {
		t.Fatalf("ids = %#v", ids)
	}
}
