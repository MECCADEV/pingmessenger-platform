package openim

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
