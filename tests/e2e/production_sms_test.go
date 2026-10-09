//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestProductionPhoneContactVerification uses an AWS SNS SMS-sandbox/test
// number and an explicitly configured SNS->SQS mirror to inspect the OTP. SNS
// itself does not expose a readable handset inbox; without the mirror this
// test intentionally skips rather than pretending delivery was verified.
func TestProductionPhoneContactVerification(t *testing.T) {
	base := os.Getenv("E2E_BASE_URL")
	phone := os.Getenv("E2E_PHONE")
	if os.Getenv("E2E_PRODUCTION") != "1" || base == "" || phone == "" || os.Getenv("E2E_SMS_MIRROR") != "1" {
		t.Skip("set E2E_PRODUCTION=1, E2E_BASE_URL, E2E_PHONE, and E2E_SMS_MIRROR=1")
	}
	inbox := newProductionInbox(t)
	inbox.drain(t)
	client := &http.Client{Timeout: 20 * time.Second}
	name := fmt.Sprintf("sms-e2e-%d", time.Now().UnixNano())
	password := "correct-horse-battery-staple"
	postJSON(t, client, base+"/v1/auth/signup", fmt.Sprintf(`{"username":%q,"password":%q,"phone":%q,"platform_id":"web"}`, name, password, phone), http.StatusCreated)
	challenge := postJSON(t, client, base+"/v1/mfa/challenge", fmt.Sprintf(`{"phone":%q,"purpose":"signup_contact_verification"}`, phone), http.StatusAccepted)
	post(t, client, base+"/v1/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, challenge["challenge_id"], inbox.otp(t)), http.StatusOK)
	access := postJSON(t, client, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, name, password), http.StatusOK)["access_token"].(string)
	factor := postAuthenticatedJSON(t, client, base+"/v1/security/mfa/factors", access, fmt.Sprintf(`{"kind":"phone","contact":%q}`, phone), http.StatusCreated)
	if factor["status"] != "enabled" {
		t.Fatalf("phone factor was not enabled: %#v", factor)
	}
	inbox.drain(t)
	pending := postJSON(t, client, base+"/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"platform_id":"web"}`, name, password), http.StatusAccepted)
	verified := postJSON(t, client, base+"/v1/auth/login/mfa/verify", fmt.Sprintf(`{"challenge_id":%q,"code":%q}`, pending["challenge_id"], inbox.otp(t)), http.StatusOK)
	if verified["access_token"] == "" || verified["refresh_token"] == "" {
		t.Fatalf("phone MFA did not issue tokens: %#v", verified)
	}
}
