package openapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// DocumentModel is Huma's OpenAPI model. Huma owns all schema generation;
// this package never serves it through the application router.
type DocumentModel = huma.OpenAPI

// schemaAdapter is intentionally inert. huma.Register invokes Handle to bind
// executable handlers for normal adapters; documentation generation only needs
// the operation metadata Huma adds before that call.
type schemaAdapter struct{}

func (schemaAdapter) Handle(*huma.Operation, func(huma.Context)) {}
func (schemaAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

type errorBody struct {
	Error string `json:"error" doc:"Human-readable failure reason."`
}

type signupBody struct {
	Username   string `json:"username,omitempty" minLength:"3" maxLength:"64" doc:"Optional unique account name."`
	Password   string `json:"password" minLength:"12" maxLength:"256" doc:"Password used for direct username/password login."`
	Nickname   string `json:"nickname,omitempty" maxLength:"128" doc:"Optional non-unique display name."`
	Email      string `json:"email,omitempty" format:"email" maxLength:"254" doc:"Optional email contact."`
	Phone      string `json:"phone,omitempty" maxLength:"32" doc:"Optional phone contact."`
	PlatformID string `json:"platform_id,omitempty" maxLength:"64" doc:"Device platform; defaults to web."`
	DeviceName string `json:"device_name,omitempty" maxLength:"128"`
	DeviceID   string `json:"device_id,omitempty" maxLength:"128"`
}
type signupInput struct{ Body signupBody }
type signupResponse struct {
	UserID       string `json:"user_id" format:"uuid"`
	PingID       string `json:"ping_id" pattern:"^[0-9]{12}$"`
	Status       string `json:"status"`
	OpenIMSync   string `json:"openim_sync" enum:"complete,pending"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	SessionID    string `json:"session_id" format:"uuid"`
}
type signupOutput struct{ Body signupResponse }

type usernameBody struct {
	Username string `json:"username" minLength:"3" maxLength:"64"`
}
type usernameInput struct{ Body usernameBody }
type usernameOutput struct {
	Body struct {
		Available bool `json:"available"`
	}
}
type usernameUpdateOutput struct {
	Body struct {
		Username string `json:"username"`
	}
}
type usernameAvailabilityWSOutput struct {
	Body struct {
		Description string `json:"description"`
	}
}

type passwordLoginBody struct {
	Username   string `json:"username,omitempty" minLength:"3" maxLength:"64" doc:"Use exactly one of username or email."`
	Email      string `json:"email,omitempty" format:"email" maxLength:"254" doc:"Use exactly one of username or email."`
	Password   string `json:"password" minLength:"12" maxLength:"256"`
	PlatformID string `json:"platform_id" maxLength:"64"`
	DeviceName string `json:"device_name,omitempty" maxLength:"128"`
	DeviceID   string `json:"device_id,omitempty" maxLength:"128"`
}
type passwordLoginInput struct{ Body passwordLoginBody }
type passwordLoginOutput struct{ Body tokenResponse }

type emailBody struct {
	Email string `json:"email" format:"email" maxLength:"254"`
}
type loginStartInput struct{ Body emailBody }
type challengeResponse struct {
	ChallengeID string `json:"challenge_id,omitempty" format:"uuid"`
	Status      string `json:"status"`
}
type loginStartOutput struct{ Body challengeResponse }

type verifyBody struct {
	ChallengeID string `json:"challenge_id" format:"uuid"`
	Code        string `json:"code" minLength:"6" maxLength:"6" pattern:"^[0-9]{6}$"`
}
type loginVerifyBody struct {
	ChallengeID string `json:"challenge_id" format:"uuid"`
	Code        string `json:"code" minLength:"6" maxLength:"6" pattern:"^[0-9]{6}$"`
	PlatformID  string `json:"platform_id" maxLength:"64"`
	DeviceName  string `json:"device_name,omitempty" maxLength:"128"`
	DeviceID    string `json:"device_id,omitempty" maxLength:"128"`
}
type loginVerifyInput struct{ Body loginVerifyBody }
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	SessionID    string `json:"session_id" format:"uuid"`
}
type loginVerifyOutput struct{ Body tokenResponse }

type refreshBody struct {
	RefreshToken string `json:"refresh_token" minLength:"32" maxLength:"512"`
}
type refreshInput struct{ Body refreshBody }
type refreshOutput struct{ Body tokenResponse }

type passwordRecoveryStartBody struct {
	Username string `json:"username,omitempty"`
	PingID   string `json:"ping_id,omitempty" pattern:"^[0-9]{12}$"`
	Email    string `json:"email,omitempty" format:"email"`
	Phone    string `json:"phone,omitempty"`
}
type passwordRecoveryStartInput struct{ Body passwordRecoveryStartBody }
type passwordChallengeBody struct {
	ChallengeID string `json:"challenge_id" format:"uuid"`
	Code        string `json:"code" minLength:"6" maxLength:"6" pattern:"^[0-9]{6}$"`
	NewPassword string `json:"new_password" minLength:"12" maxLength:"256"`
}
type passwordChallengeInput struct{ Body passwordChallengeBody }
type passwordChangeBody struct {
	CurrentPassword string `json:"current_password" minLength:"12" maxLength:"256"`
	NewPassword     string `json:"new_password" minLength:"12" maxLength:"256"`
}
type passwordChangeInput struct{ Body passwordChangeBody }
type passwordChangeVerifyInput struct{ Body passwordChallengeBody }
type passwordStatusOutput struct {
	Body struct {
		Status      string `json:"status"`
		ChallengeID string `json:"challenge_id,omitempty" format:"uuid"`
	}
}

type challengeBody struct {
	Email   string `json:"email,omitempty" format:"email" maxLength:"254"`
	Phone   string `json:"phone,omitempty" maxLength:"32"`
	Purpose string `json:"purpose" enum:"signup_contact_verification,login,step_up"`
}
type challengeInput struct{ Body challengeBody }
type challengeOutput struct{ Body challengeResponse }
type mfaFactorBody struct {
	ID           string `json:"id" format:"uuid"`
	Kind         string `json:"kind"`
	ContactValue string `json:"contact_value,omitempty"`
	Preferred    bool   `json:"preferred"`
}
type mfaListOutput struct {
	Body struct {
		Factors []mfaFactorBody `json:"factors"`
	}
}
type enrollMFAInput struct {
	Body struct {
		Kind      string `json:"kind" enum:"email,phone,totp"`
		ContactID string `json:"contact_id,omitempty" format:"uuid"`
		Contact   string `json:"contact,omitempty"`
		Label     string `json:"label,omitempty"`
	}
}
type enrollMFAOutput struct {
	Body struct {
		FactorID string `json:"factor_id" format:"uuid"`
		Kind     string `json:"kind"`
		Secret   string `json:"secret,omitempty"`
		Status   string `json:"status"`
	}
}
type verifyMFAEnrollmentInput struct {
	Body struct {
		FactorID string `json:"factor_id" format:"uuid"`
		Code     string `json:"code" minLength:"6" maxLength:"6"`
	}
}
type disableMFAInput struct {
	Body struct {
		FactorID    string `json:"factor_id" format:"uuid"`
		ChallengeID string `json:"challenge_id" format:"uuid"`
		Code        string `json:"code" minLength:"6" maxLength:"6"`
	}
}
type loginMFAVerifyInput struct {
	Body struct {
		ChallengeID string `json:"challenge_id" format:"uuid"`
		Code        string `json:"code" minLength:"6" maxLength:"6"`
	}
}
type verifyInput struct{ Body verifyBody }
type verifyOutput struct {
	Body struct {
		Status string `json:"status"`
		UserID string `json:"user_id" format:"uuid"`
	}
}

type remainingOutput struct {
	Body struct {
		Remaining int `json:"remaining" minimum:"0"`
	}
}
type regenerateInput struct{ Body verifyBody }
type regenerateOutput struct {
	Body struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
}
type activityOutput struct {
	Body struct {
		Sessions []session `json:"sessions"`
	}
}
type session struct {
	ID           string `json:"id" format:"uuid"`
	PlatformID   string `json:"platform_id"`
	DeviceName   string `json:"device_name"`
	DeviceID     string `json:"device_id"`
	LastActiveAt string `json:"last_active_at" format:"date-time"`
}
type revokeBody struct {
	SessionIDs []string `json:"session_ids,omitempty" maxItems:"100" items:"uuid"`
	All        bool     `json:"all"`
}
type revokeInput struct{ Body revokeBody }
type emptyOutput struct{}

type deviceJWK struct {
	Kty string `json:"kty" enum:"EC"`
	Crv string `json:"crv" enum:"P-256"`
	X   string `json:"x"`
	Y   string `json:"y"`
}
type registerDeviceKeyInput struct {
	Body struct {
		PublicKey deviceJWK `json:"public_key"`
	}
}
type registerDeviceKeyOutput struct {
	Body struct {
		Fingerprint string `json:"fingerprint"`
		Status      string `json:"status"`
	}
}
type openIMChallengeOutput struct {
	Body struct {
		ChallengeID string `json:"challenge_id" format:"uuid"`
		Nonce       string `json:"nonce"`
		Audience    string `json:"audience"`
		ExpiresAt   string `json:"expires_at" format:"date-time"`
	}
}
type issueOpenIMTokenInput struct {
	Body struct {
		ChallengeID string `json:"challenge_id" format:"uuid"`
		Nonce       string `json:"nonce"`
		Signature   string `json:"signature"`
		Fingerprint string `json:"fingerprint"`
	}
}
type issueOpenIMTokenOutput struct {
	Body struct {
		Token      string `json:"token"`
		PlatformID int32  `json:"platform_id"`
		ExpiresAt  string `json:"expires_at" format:"date-time"`
	}
}

type profileOutput struct {
	Body struct {
		Username string  `json:"username,omitempty"`
		PingID   string  `json:"ping_id"`
		Image    *string `json:"image" format:"uri"`
		Nickname string  `json:"nickname"`
	}
}
type profileUpdateInput struct {
	RawBody huma.MultipartFormFiles[struct {
		Image    huma.FormFile `form:"image" contentType:"image/jpeg,image/png,image/webp"`
		Nickname string        `form:"nickname" maxLength:"128"`
	}]
}
type profileUpdateOutput struct {
	Body struct {
		Image    *string `json:"image" format:"uri"`
		Nickname string  `json:"nickname"`
		Sync     string  `json:"sync" enum:"complete,pending"`
	}
}
type discoverBody struct {
	Emails   []string `json:"emails,omitempty" maxItems:"100" items:"email"`
	MobileNo []string `json:"mobile_no,omitempty" maxItems:"100"`
}
type discoverInput struct{ Body discoverBody }
type discoverOutput struct {
	Body struct {
		OpenIMUserIDs []string `json:"open_im_user_ids"`
	}
}

func newDocument() (*DocumentModel, error) {
	config := huma.DefaultConfig("PingMessenger API", "0.6.0")
	// Disable all Huma HTTP endpoints. The running FastHTTP router is unchanged.
	config.OpenAPIPath = ""
	config.DocsPath = ""
	config.SchemasPath = ""
	api := huma.NewAPI(config, schemaAdapter{})
	api.OpenAPI().Info.Description = "Schema-only API contract. Runtime routes are implemented by FastHTTP."
	api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
	}

	public := []map[string][]string{{}}
	bearer := []map[string][]string{{"bearerAuth": {}}}
	register[signupInput, signupOutput](api, operation(http.MethodPost, "/v1/auth/signup", "signUp", "Auth", "Create an account. Username is optional and unique when supplied; signup returns device-bound tokens.", 201, public))
	register[usernameInput, usernameOutput](api, operation(http.MethodPost, "/v1/auth/verify-username", "verifyUsername", "Auth", "Check whether a username is available.", 200, public))
	register[struct{}, usernameAvailabilityWSOutput](api, operation(http.MethodGet, "/v1/auth/username/availability", "usernameAvailabilityWebSocket", "Auth", "Upgrade to a WebSocket and send check_username JSON frames for availability results.", 101, public))
	register[passwordLoginInput, passwordLoginOutput](api, operation(http.MethodPost, "/v1/auth/login", "passwordLogin", "Auth", "Create a device session using exactly one of username or email plus a password. Email is optional at signup.", 200, public))
	register[loginStartInput, loginStartOutput](api, operation(http.MethodPost, "/v1/auth/login/start", "startLogin", "Auth", "Start passwordless email login.", 202, public))
	register[loginVerifyInput, loginVerifyOutput](api, operation(http.MethodPost, "/v1/auth/login/verify", "verifyLogin", "Auth", "Verify a passwordless login code and create a session.", 200, public))
	register[refreshInput, refreshOutput](api, operation(http.MethodPost, "/v1/auth/refresh", "refreshSession", "Auth", "Rotate a refresh token.", 200, public))
	register[passwordRecoveryStartInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/recovery/start", "startPasswordRecovery", "Auth", "Start privacy-preserving password recovery by username, ping ID, email, or phone.", 202, public))
	register[passwordChallengeInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/recovery/complete", "completePasswordRecovery", "Auth", "Complete password recovery with a one-time code.", 200, public))
	register[passwordChangeInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/change", "changePassword", "Auth", "Change a password; verified contacts require a step-up code.", 200, bearer))
	register[passwordChangeVerifyInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/change/verify", "verifyPasswordChange", "Auth", "Complete contact step-up for a password change.", 200, bearer))
	register[challengeInput, challengeOutput](api, operation(http.MethodPost, "/v1/mfa/challenge", "issueMFAChallenge", "MFA", "Issue an email verification or step-up challenge.", 202, public))
	register[verifyInput, verifyOutput](api, operation(http.MethodPost, "/v1/mfa/verify", "verifyMFAChallenge", "MFA", "Verify a non-login MFA challenge.", 200, public))
	register[loginMFAVerifyInput, loginVerifyOutput](api, operation(http.MethodPost, "/v1/auth/login/mfa/verify", "verifyLoginMFA", "Auth", "Complete one enabled MFA factor before creating a login session.", 200, public))
	register[struct{}, mfaListOutput](api, operation(http.MethodGet, "/v1/security/mfa", "listMFA", "Security", "List enabled account MFA factors.", 200, bearer))
	register[enrollMFAInput, enrollMFAOutput](api, operation(http.MethodPost, "/v1/security/mfa/factors", "enrollMFA", "Security", "Enroll an email, phone, or TOTP factor.", 201, bearer))
	register[verifyMFAEnrollmentInput, enrollMFAOutput](api, operation(http.MethodPost, "/v1/security/mfa/factors/verify", "verifyMFAEnrollment", "Security", "Confirm TOTP enrollment.", 200, bearer))
	register[disableMFAInput, emptyOutput](api, operation(http.MethodPost, "/v1/security/mfa/factors/disable", "disableMFA", "Security", "Disable an enabled MFA factor.", 200, bearer))
	register[struct{}, remainingOutput](api, operation(http.MethodGet, "/v1/security/backup-codes", "getBackupCodeStatus", "Security", "Get remaining backup-code count.", 200, bearer))
	register[regenerateInput, regenerateOutput](api, operation(http.MethodPost, "/v1/security/backup-codes/regenerate", "regenerateBackupCodes", "Security", "Replace backup codes after a step-up challenge.", 200, bearer))
	register[struct{}, activityOutput](api, operation(http.MethodGet, "/v1/security/activity", "listSecurityActivity", "Security", "List active and historic sessions.", 200, bearer))
	register[revokeInput, emptyOutput](api, operation(http.MethodPost, "/v1/security/revoke", "revokeSessions", "Security", "Revoke selected sessions or all other sessions.", 204, bearer))
	register[registerDeviceKeyInput, registerDeviceKeyOutput](api, operation(http.MethodPost, "/v1/openim/device-key", "registerOpenIMDeviceKey", "OpenIM", "Register or rotate this device's P-256 public key. Private keys remain on the device.", 200, bearer))
	register[struct{}, openIMChallengeOutput](api, operation(http.MethodPost, "/v1/openim/token/challenge", "createOpenIMTokenChallenge", "OpenIM", "Create a session-bound device-proof challenge.", 201, bearer))
	register[issueOpenIMTokenInput, issueOpenIMTokenOutput](api, operation(http.MethodPost, "/v1/openim/token/issue", "issueOpenIMToken", "OpenIM", "Verify the signed device proof and issue an OpenIM token for this device.", 200, bearer))
	register[struct{}, profileOutput](api, operation(http.MethodGet, "/v1/profile/", "getProfile", "Profile", "Get the authenticated user's profile asset URL.", 200, bearer))
	register[usernameInput, usernameUpdateOutput](api, operation(http.MethodPatch, "/v1/profile/username", "updateUsername", "Profile", "Set or change the optional unique username.", 200, bearer))
	profileUpdate := operation(http.MethodPost, "/v1/profile/update", "updateProfile", "Profile", "Update a non-unique nickname and/or upload an image as multipart/form-data (JPEG, PNG, or WebP; max 5 MiB).", 200, bearer)
	profileUpdate.MaxBodyBytes = 5 << 20
	register[profileUpdateInput, profileUpdateOutput](api, profileUpdate)
	register[discoverInput, discoverOutput](api, operation(http.MethodPost, "/v1/friends/discover-network", "discoverNetwork", "Friends", "Discover registered contacts from supplied email addresses or phone numbers.", 200, bearer))

	return api.OpenAPI(), nil
}

func register[I, O any](api huma.API, op huma.Operation) {
	huma.Register(api, op, func(context.Context, *I) (*O, error) { return nil, nil })
}

func operation(method, path, id, tag, description string, status int, security []map[string][]string) huma.Operation {
	return huma.Operation{
		Method: method, Path: path, OperationID: id, Tags: []string{tag}, Description: description,
		DefaultStatus: status, Errors: []int{http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusInternalServerError}, Security: security,
	}
}
