//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestProductionSESMFAWithMirror verifies the live SES sender call in the
// cluster. SES sandbox permits simulator recipients; the explicitly enabled
// Mailpit mirror makes the OTP inspectable without copying real user messages
// in normal production configuration.
func TestProductionSESMFAWithMirror(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	mailpit := os.Getenv("E2E_MAILPIT_URL")
	recipient := os.Getenv("E2E_SES_RECIPIENT")
	if os.Getenv("E2E_PRODUCTION") != "1" || base == "" || mailpit == "" || recipient == "" {
		t.Skip("set E2E_PRODUCTION=1, E2E_BASE_URL, E2E_MAILPIT_URL, and a verified/SES-simulator E2E_SES_RECIPIENT")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	name := fmt.Sprintf("ses-e2e-%d", time.Now().UnixNano())
	password := "correct-horse-battery-staple"
	postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q,"email":%q,"platform_id":"web"}`, name, password, recipient), http.StatusCreated)
	challenge := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"email":%q,"purpose":"signup_contact_verification"}`, recipient), http.StatusAccepted)
	code := mailpitCode(t, client, mailpit, recipient, "verification")
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, challenge["challenge_id"], code), http.StatusOK)
	login := postJSON(t, client, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, name, password), http.StatusOK)
	access := login["access_token"].(string)
	factor := postAuthenticatedJSON(t, client, base+"/v1/security/mfa/factors", access, fmt.Sprintf(`{"kind":"email","contact":%q}`, recipient), http.StatusCreated)
	if factor["status"] != "enabled" {
		t.Fatalf("email factor was not enabled: %#v", factor)
	}
	pending := postJSON(t, client, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, name, password), http.StatusAccepted)
	verified := postJSON(t, client, base+"/v1/auth/login/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, pending["challenge_id"], mailpitCode(t, client, mailpit, recipient, "login")), http.StatusOK)
	if verified["access_token"] == "" || verified["refresh_token"] == "" {
		t.Fatalf("SES email MFA did not issue tokens: %#v", verified)
	}
}

func mailpitCode(t *testing.T, client *http.Client, base, recipient, subject string) string {
	t.Helper()
	re := regexp.MustCompile(`\b\d{6}\b`)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(strings.TrimRight(base, "/") + "/api/v1/messages")
		if err == nil {
			var payload struct {
				Messages []struct {
					To               []struct{ Address string }
					Subject, Snippet string
				} `json:"messages"`
			}
			if json.NewDecoder(response.Body).Decode(&payload) == nil {
				response.Body.Close()
				for i := len(payload.Messages) - 1; i >= 0; i-- {
					m := payload.Messages[i]
					if m.Subject == "PingMessenger "+subject+" code" || (subject == "verification" && m.Subject == "PingMessenger verification code") {
						for _, to := range m.To {
							if to.Address == recipient {
								if code := re.FindString(m.Snippet); code != "" {
									return code
								}
							}
						}
					}
				}
			} else {
				response.Body.Close()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no %s OTP mirrored for %s", subject, recipient)
	return ""
}
