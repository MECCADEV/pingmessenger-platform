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
	Username string `json:"username" minLength:"3" maxLength:"64" doc:"Unique account name."`
	Password string `json:"password" minLength:"12" maxLength:"256" doc:"Password; accepted only during signup."`
	Email    string `json:"email,omitempty" format:"email" maxLength:"254" doc:"Optional email contact."`
	Phone    string `json:"phone,omitempty" maxLength:"32" doc:"Optional phone contact."`
}
type signupInput struct{ Body signupBody }
type signupResponse struct {
	UserID     string `json:"user_id" format:"uuid"`
	Status     string `json:"status"`
	OpenIMSync string `json:"openim_sync" enum:"complete,pending"`
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

type challengeBody struct {
	Email   string `json:"email" format:"email" maxLength:"254"`
	Purpose string `json:"purpose" enum:"signup_contact_verification,login,step_up"`
}
type challengeInput struct{ Body challengeBody }
type challengeOutput struct{ Body challengeResponse }
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

type profileOutput struct {
	Body struct {
		Image *string `json:"image" format:"uri"`
	}
}
type profileUpdateInput struct {
	RawBody huma.MultipartFormFiles[struct {
		Image huma.FormFile `form:"image" contentType:"image/jpeg,image/png,image/webp" required:"true"`
	}]
}
type profileUpdateOutput struct {
	Body struct {
		Image string `json:"image" format:"uri"`
		Sync  string `json:"sync" enum:"complete,pending"`
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
	config := huma.DefaultConfig("PingMessenger API", "0.1.0")
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
	register[signupInput, signupOutput](api, operation(http.MethodPost, "/v1/auth/signup", "signUp", "Auth", "Create an account. Username and password are accepted only here.", 201, public))
	register[usernameInput, usernameOutput](api, operation(http.MethodPost, "/v1/auth/verify-username", "verifyUsername", "Auth", "Check whether a username is available.", 200, public))
	register[loginStartInput, loginStartOutput](api, operation(http.MethodPost, "/v1/auth/login/start", "startLogin", "Auth", "Start passwordless email login.", 202, public))
	register[loginVerifyInput, loginVerifyOutput](api, operation(http.MethodPost, "/v1/auth/login/verify", "verifyLogin", "Auth", "Verify a passwordless login code and create a session.", 200, public))
	register[refreshInput, refreshOutput](api, operation(http.MethodPost, "/v1/auth/refresh", "refreshSession", "Auth", "Rotate a refresh token.", 200, public))
	register[challengeInput, challengeOutput](api, operation(http.MethodPost, "/v1/mfa/challenge", "issueMFAChallenge", "MFA", "Issue an email verification or step-up challenge.", 202, public))
	register[verifyInput, verifyOutput](api, operation(http.MethodPost, "/v1/mfa/verify", "verifyMFAChallenge", "MFA", "Verify a non-login MFA challenge.", 200, public))
	register[struct{}, remainingOutput](api, operation(http.MethodGet, "/v1/security/backup-codes", "getBackupCodeStatus", "Security", "Get remaining backup-code count.", 200, bearer))
	register[regenerateInput, regenerateOutput](api, operation(http.MethodPost, "/v1/security/backup-codes/regenerate", "regenerateBackupCodes", "Security", "Replace backup codes after a step-up challenge.", 200, bearer))
	register[struct{}, activityOutput](api, operation(http.MethodGet, "/v1/security/activity", "listSecurityActivity", "Security", "List active and historic sessions.", 200, bearer))
	register[revokeInput, emptyOutput](api, operation(http.MethodPost, "/v1/security/revoke", "revokeSessions", "Security", "Revoke selected sessions or all other sessions.", 204, bearer))
	register[struct{}, profileOutput](api, operation(http.MethodGet, "/v1/profile/", "getProfile", "Profile", "Get the authenticated user's profile asset URL.", 200, bearer))
	profileUpdate := operation(http.MethodPost, "/v1/profile/update", "updateProfile", "Profile", "Upload an image form field as multipart/form-data (JPEG, PNG, or WebP; max 5 MiB).", 200, bearer)
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
