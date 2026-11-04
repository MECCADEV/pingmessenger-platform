package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp"
)

func TestNormalizePasswordLoginIdentifiers(t *testing.T) {
	username, email, pingID := normalizePasswordLoginIdentifiers(passwordLoginRequest{
		Username: "  Alice.Example ",
		Email:    "  Person@Example.TEST ",
		PingID:   " 001234567890 ",
	})
	if username != "alice.example" || email != "person@example.test" || pingID != "001234567890" {
		t.Fatalf("normalized identifiers = (%q, %q, %q)", username, email, pingID)
	}
}

func TestPasswordRoutesAcceptSixAndRejectFiveCharacters(t *testing.T) {
	validate := validator.New()
	testCases := []struct {
		name string
		make func(string) any
	}{
		{"signup", func(password string) any { return signupRequest{Username: "alice", Password: password} }},
		{"login", func(password string) any {
			return passwordLoginRequest{Username: "alice", Password: password, PlatformID: "web"}
		}},
		{"change", func(password string) any { return passwordChangeRequest{CurrentPassword: "old", NewPassword: password} }},
		{"recovery-complete", func(password string) any {
			return passwordRecoveryCompleteRequest{ChallengeID: "00000000-0000-4000-8000-000000000001", Code: "123456", NewPassword: password}
		}},
		{"change-verify", func(password string) any {
			return passwordChangeVerifyRequest{ChallengeID: "00000000-0000-4000-8000-000000000001", Code: "123456", NewPassword: password}
		}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate.Struct(tc.make("12345")); err == nil {
				t.Fatal("five-character password passed validation")
			}
			if err := validate.Struct(tc.make("123456")); err != nil {
				t.Fatalf("six-character password rejected: %v", err)
			}
			if err := validate.Struct(tc.make(strings.Repeat("a", 257))); err == nil {
				t.Fatal("257-character password passed validation")
			}
		})
	}
}

func TestPasswordChangeKeepsShortLegacyCurrentPasswordEligible(t *testing.T) {
	if err := validator.New().Struct(passwordChangeRequest{CurrentPassword: "x", NewPassword: "123456"}); err != nil {
		t.Fatalf("short legacy current password rejected: %v", err)
	}
}

func TestPasswordLoginThrottleResponseIncludesRetrySeconds(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	writePasswordLoginThrottle(ctx, 900)
	if ctx.Response.StatusCode() != fasthttp.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", ctx.Response.StatusCode())
	}
	if got := string(ctx.Response.Header.Peek("Retry-After")); got != "900" {
		t.Fatalf("Retry-After=%q, want 900", got)
	}
	var body struct {
		RetryAfterSeconds int `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("decode throttle response: %v", err)
	}
	if body.RetryAfterSeconds != 900 {
		t.Fatalf("retry_after_seconds=%d, want 900", body.RetryAfterSeconds)
	}
}
