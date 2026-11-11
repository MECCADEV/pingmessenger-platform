package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/documents"
)

func TestTermsAcceptanceTrackingDoesNotGateChatStoriesAndFriends(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	userID, sessionID := uuid.NewString(), uuid.NewString()
	username := "terms-" + strings.ReplaceAll(userID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status) VALUES($1,$2,'test','active')`, userID, username); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })
	if _, err = pool.Exec(ctx, `INSERT INTO device_sessions(id,user_id,refresh_token_hash,platform_id,expires_at) VALUES($1,$2,$3,'ios',now()+interval '1 hour')`, sessionID, userID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokenManager(strings.Repeat("t", 40), "terms-test", time.Hour, time.Hour)
	access, _, err := tokens.IssueAccess(userID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	api := NewWithDocuments(pool, nil, nil, nil, strings.Repeat("o", 40), tokens, nil, nil, nil, "", "", documents.DefaultPolicy())
	api.ConfigureTermsRequiredVersion("2026-11-10")
	request := func(handler fasthttp.RequestHandler, method, path, body string) *fasthttp.RequestCtx {
		t.Helper()
		requestCtx := &fasthttp.RequestCtx{}
		requestCtx.Request.Header.SetMethod(method)
		requestCtx.Request.SetRequestURI(path)
		requestCtx.Request.Header.Set("Authorization", "Bearer "+access)
		if body != "" {
			requestCtx.Request.SetBodyString(body)
		}
		handler(requestCtx)
		return requestCtx
	}
	loginResponse := func() map[string]any {
		t.Helper()
		loginCtx := &fasthttp.RequestCtx{}
		api.issueLoginSession(loginCtx, userID, "ios", "terms-test", "device-1")
		if loginCtx.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("login response status=%d body=%s", loginCtx.Response.StatusCode(), loginCtx.Response.Body())
		}
		var body map[string]any
		if err := json.Unmarshal(loginCtx.Response.Body(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	login := loginResponse()
	if login["terms_accepted_version"] != nil || login["terms_required_version"] != "2026-11-10" {
		t.Fatalf("unaccepted login terms=%v", login)
	}
	for _, tc := range []struct {
		name               string
		handler            fasthttp.RequestHandler
		method, path, body string
	}{
		{"chat token", api.issueOpenIMToken, "POST", "/v1/openim/token/issue", `{}`},
		{"story create", api.createStory, "POST", "/v1/stories", ""},
		{"friend search", api.searchFriends, "POST", "/v1/friends/search", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := request(tc.handler, tc.method, tc.path, tc.body)
			if got.Response.StatusCode() == fasthttp.StatusForbidden && strings.Contains(string(got.Response.Body()), "terms_not_accepted") {
				t.Fatalf("legacy request was blocked by terms gate: status=%d body=%s", got.Response.StatusCode(), got.Response.Body())
			}
		})
	}
	profile := request(api.profile, "GET", "/v1/profile/", "")
	if profile.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("profile status=%d body=%s", profile.Response.StatusCode(), profile.Response.Body())
	}
	var profileBody map[string]any
	if err = json.Unmarshal(profile.Response.Body(), &profileBody); err != nil || profileBody["terms_accepted_version"] != nil || profileBody["terms_required_version"] != "2026-11-10" {
		t.Fatalf("profile terms=%v err=%v", profileBody, err)
	}
	stale := request(api.acceptTerms, "POST", "/v1/account/terms", `{"terms_version":"2026-11-09"}`)
	if stale.Response.StatusCode() != fasthttp.StatusConflict || !strings.Contains(string(stale.Response.Body()), "terms_version_not_current") {
		t.Fatalf("stale terms status=%d body=%s", stale.Response.StatusCode(), stale.Response.Body())
	}
	accepted := request(api.acceptTerms, "POST", "/v1/account/terms", `{"terms_version":"2026-11-10"}`)
	if accepted.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("accept status=%d body=%s", accepted.Response.StatusCode(), accepted.Response.Body())
	}
	login = loginResponse()
	if login["terms_accepted_version"] != "2026-11-10" || login["terms_required_version"] != "2026-11-10" {
		t.Fatalf("accepted login terms=%v", login)
	}
	for _, tc := range []struct {
		name               string
		handler            fasthttp.RequestHandler
		method, path, body string
	}{
		{"chat token", api.issueOpenIMToken, "POST", "/v1/openim/token/issue", `{}`},
		{"story create", api.createStory, "POST", "/v1/stories", ""},
		{"friend search", api.searchFriends, "POST", "/v1/friends/search", `{}`},
	} {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			got := request(tc.handler, tc.method, tc.path, tc.body)
			if got.Response.StatusCode() == fasthttp.StatusForbidden {
				t.Fatalf("still gated after acceptance: %s", got.Response.Body())
			}
		})
	}
	var acceptedVersion *string
	var acceptedAt *time.Time
	if err = pool.QueryRow(ctx, `SELECT terms_accepted_version,terms_accepted_at FROM users WHERE id=$1`, userID).Scan(&acceptedVersion, &acceptedAt); err != nil || acceptedVersion == nil || *acceptedVersion != "2026-11-10" || acceptedAt == nil {
		t.Fatalf("accepted version=%v at=%v err=%v", acceptedVersion, acceptedAt, err)
	}
}
