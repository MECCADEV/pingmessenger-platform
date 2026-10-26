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
	Username   string `json:"username,omitempty" minLength:"3" maxLength:"64" doc:"Use exactly one of username, email, or ping_id."`
	Email      string `json:"email,omitempty" format:"email" maxLength:"254" doc:"Use exactly one of username, email, or ping_id."`
	PingID     string `json:"ping_id,omitempty" pattern:"^[0-9]{12}$" doc:"Use exactly one of username, email, or ping_id."`
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
	CurrentPassword string `json:"current_password" maxLength:"256" doc:"The existing password; mismatch always returns 401, including short legacy passwords."`
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
type addContactInput struct {
	Body struct {
		Kind  string `json:"kind" enum:"email,phone"`
		Value string `json:"value" maxLength:"254"`
	}
}
type addContactOutput struct {
	Body struct {
		ContactID   string `json:"contact_id" format:"uuid"`
		Kind        string `json:"kind"`
		Value       string `json:"value"`
		ChallengeID string `json:"challenge_id" format:"uuid"`
		Status      string `json:"status"`
	}
}
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
type profileImageDeleteOutput struct {
	Body struct {
		Image *string `json:"image" format:"uri"`
		Sync  string  `json:"sync" enum:"complete,pending"`
	}
}
type accountDeleteInput struct {
	Body struct {
		CurrentPassword string `json:"current_password" maxLength:"256"`
		AcceptTerms     bool   `json:"accept_terms"`
	}
}
type accountDeleteOutput struct {
	Body struct {
		Status    string `json:"status"`
		DeletedAt string `json:"deleted_at" format:"date-time"`
	}
}
type story struct {
	ID              string  `json:"id" format:"uuid"`
	OwnerID         string  `json:"owner_id" format:"uuid"`
	MediaType       string  `json:"media_type" enum:"image,video,text"`
	MediaURL        string  `json:"media_url" format:"uri"`
	ContentType     string  `json:"content_type"`
	ByteSize        int64   `json:"byte_size"`
	Caption         string  `json:"caption"`
	Text            string  `json:"text,omitempty" maxLength:"700"`
	BackgroundColor string  `json:"background_color,omitempty" pattern:"^#[0-9A-Fa-f]{6}$"`
	Visibility      string  `json:"visibility" enum:"contacts,allow,exclude"`
	ExpiresAt       string  `json:"expires_at" format:"date-time"`
	DeletedAt       *string `json:"deleted_at,omitempty" format:"date-time"`
	CreatedAt       string  `json:"created_at" format:"date-time"`
}
type storyCreateInput struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"128" doc:"Optional owner-scoped key for safe retry of the same logical story create. Use 1–128 visible non-whitespace ASCII characters; retained for 30 days."`
	RawBody        huma.MultipartFormFiles[struct {
		Media            huma.FormFile `form:"media" contentType:"image/*,video/*"`
		Type             string        `form:"type" enum:"image,video,text"`
		Text             string        `form:"text" maxLength:"700"`
		BackgroundColor  string        `form:"background_color" pattern:"^#[0-9A-Fa-f]{6}$"`
		Caption          string        `form:"caption" maxLength:"4096"`
		Visibility       string        `form:"visibility" enum:"contacts,allow,exclude"`
		UserIDs          string        `form:"user_ids" doc:"JSON array or comma-separated Platform account UUIDs for selected visibility. These are not OpenIM IDs; resolve OpenIM friend IDs through GET /v1/friends fields openim_user_id and user_id."`
		ExpiresInSeconds int           `form:"expires_in_seconds" minimum:"60" maximum:"604800"`
	}]
}
type storyCreateOutput struct {
	Body struct {
		Story story `json:"story"`
	}
}
type storyDeleteOutput struct {
	Body struct {
		Story story `json:"story"`
	}
}
type storyViewInput struct {
	StoryID string `path:"storyID" format:"uuid"`
	Body    struct {
		Reaction string `json:"reaction,omitempty" maxLength:"64"`
	}
}
type storyViewOutput struct {
	Body struct {
		View struct {
			UserID   string `json:"user_id" format:"uuid"`
			ViewedAt string `json:"viewed_at" format:"date-time"`
			Reaction string `json:"reaction"`
		} `json:"view"`
	}
}
type storyViewsInput struct {
	StoryID string `path:"storyID" format:"uuid"`
}
type storyViewsOutput struct {
	Body struct {
		Views []struct {
			UserID   string `json:"user_id" format:"uuid"`
			ViewedAt string `json:"viewed_at" format:"date-time"`
			Reaction string `json:"reaction"`
		} `json:"views"`
	}
}
type storyDeleteInput struct {
	StoryID string `path:"storyID" format:"uuid"`
}
type storyWebSocketOutput struct {
	Body struct {
		Description string `json:"description"`
	}
}
type friendProfile struct {
	UserID       string  `json:"user_id" format:"uuid"`
	OpenIMUserID string  `json:"openim_user_id"`
	PingID       string  `json:"ping_id" pattern:"^[0-9]{12}$"`
	Username     string  `json:"username,omitempty"`
	Nickname     string  `json:"nickname"`
	Image        *string `json:"image" format:"uri"`
}
type friendsOutput struct {
	Body struct {
		Friends []friendProfile `json:"friends"`
	}
}
type blockItem struct {
	UserID    string  `json:"user_id" format:"uuid"`
	PingID    string  `json:"ping_id" pattern:"^[0-9]{12}$"`
	Nickname  string  `json:"nickname"`
	Image     *string `json:"image" format:"uri"`
	BlockedAt string  `json:"blocked_at" format:"date-time"`
}
type blocksOutput struct {
	Body struct {
		Blocks []blockItem `json:"blocks"`
	}
}
type blockCreatedOutput struct {
	Body struct {
		PingID    string `json:"ping_id" pattern:"^[0-9]{12}$"`
		BlockedAt string `json:"blocked_at" format:"date-time"`
	}
}
type blockInput struct {
	Body struct {
		PingID string `json:"ping_id" pattern:"^[0-9]{12}$"`
	}
}
type blockPathInput struct {
	PingID string `path:"pingID" pattern:"^[0-9]{12}$"`
}
type document struct {
	ID          string `json:"id" format:"uuid"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	FileSize    int64  `json:"file_size"`
	SourceURL   string `json:"source_url" format:"uri"`
	CreatedAt   string `json:"created_at" format:"date-time"`
	ExpiresAt   string `json:"expires_at" format:"date-time"`
}
type documentUploadInput struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"128" doc:"Optional owner-scoped key. Reuse the same key and exact file on retry to receive the original document instead of creating a duplicate. Reusing it for different content returns 409; replay after deletion or expiry returns 410. The key remains bound to its document tombstone."`
	RawBody        huma.MultipartFormFiles[struct {
		Document huma.FormFile `form:"document" contentType:"application/pdf,application/rtf,application/msword,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,application/vnd.ms-powerpoint,application/vnd.openxmlformats-officedocument.presentationml.presentation,text/plain,text/csv,application/zip"`
	}]
}
type documentUploadOutput struct {
	Body struct {
		Document document `json:"document"`
		FileElem struct {
			SourceURL string `json:"sourceUrl" format:"uri"`
			FileName  string `json:"fileName"`
			FileSize  int64  `json:"fileSize"`
			UUID      string `json:"uuid" format:"uuid"`
		} `json:"fileElem"`
	}
}
type documentPathInput struct {
	DocumentID string `path:"documentID" format:"uuid"`
}
type documentMessageLinkInput struct {
	DocumentID string `path:"documentID" format:"uuid"`
	Body       struct {
		ConversationID string `json:"conversation_id" maxLength:"256"`
		Seq            int64  `json:"seq" minimum:"1"`
	}
}
type documentGetOutput struct {
	Body struct {
		Document document `json:"document"`
	}
}
type discoverBody struct {
	Emails   []string `json:"emails,omitempty" maxItems:"100"`
	MobileNo []string `json:"mobile_no,omitempty" maxItems:"100"`
}
type discoverInput struct{ Body discoverBody }
type discoverOutput struct {
	Body struct {
		OpenIMUserIDs []string `json:"open_im_user_ids"`
	}
}
type friendSearchBody struct {
	Query string `json:"query" minLength:"1" maxLength:"254" doc:"Username fragment, exact email/phone, or exact 12-digit PingID."`
	Field string `json:"field" enum:"username,email,ping_id,phone"`
	Limit int    `json:"limit,omitempty" minimum:"1" maximum:"50" default:"20"`
}
type friendSearchInput struct{ Body friendSearchBody }
type friendSearchItem struct {
	UserID       string  `json:"userID" doc:"OpenIM user identity."`
	Nickname     string  `json:"nickname"`
	FaceURL      *string `json:"faceURL,omitempty" format:"uri"`
	Remark       string  `json:"remark"`
	Relationship int     `json:"relationship" doc:"-1 means the platform directory did not evaluate OpenIM friendship or blacklist state."`
	Username     string  `json:"username"`
	PingID       string  `json:"ping_id" pattern:"^[0-9]{12}$"`
}
type friendSearchOutput struct {
	Body struct {
		Results []*friendSearchItem `json:"results"`
		Total   int                 `json:"total" minimum:"0"`
	}
}

type paymentCreateBody struct {
	PaymentID     string `json:"payment_id" minLength:"1" maxLength:"128"`
	ToUserID      string `json:"to_user_id" format:"uuid"`
	PaymentType   string `json:"payment_type" enum:"direct,fund_campaign"`
	ChainID       string `json:"chain_id" maxLength:"64"`
	TransactionID string `json:"transaction_id" maxLength:"256"`
	Asset         string `json:"asset,omitempty" maxLength:"64"`
	AmountAtomic  string `json:"amount_atomic" pattern:"^[0-9]+$"`
	ExpiresInSec  int    `json:"expires_in_seconds,omitempty" minimum:"30" maximum:"86400"`
}
type paymentCreateInput struct {
	ConversationID string `path:"conversationID" maxLength:"128"`
	Body           paymentCreateBody
}
type payment struct {
	PaymentID      string `json:"payment_id"`
	ConversationID string `json:"conversation_id"`
	FromUserID     string `json:"from_user_id" format:"uuid"`
	ToUserID       string `json:"to_user_id" format:"uuid"`
	PaymentType    string `json:"payment_type"`
	Status         string `json:"status"`
	UpdateReason   string `json:"update_reason,omitempty" doc:"timeout when the platform marked an unreported transaction failed"`
	ChainID        string `json:"chain_id"`
	TransactionID  string `json:"transaction_id"`
	BlockID        string `json:"block_id,omitempty"`
	Asset          string `json:"asset,omitempty"`
	AmountAtomic   string `json:"amount_atomic"`
	Version        int64  `json:"version"`
	ExpiresAt      string `json:"expires_at" format:"date-time"`
	CreatedAt      string `json:"created_at" format:"date-time"`
	UpdatedAt      string `json:"updated_at" format:"date-time"`
}
type paymentCreateOutput struct {
	Body struct {
		Payment       payment `json:"payment"`
		WatchStatus   string  `json:"watch_status"`
		MessageStatus string  `json:"message_status"`
	}
}
type paymentGetOutput struct {
	Body struct {
		Payment payment `json:"payment"`
	}
}
type paymentChainOutput struct {
	Body struct {
		Transaction chainTransactionSchema `json:"transaction"`
		Transfers   []chainTransferSchema  `json:"transfers"`
	}
}
type chainTransactionSchema struct {
	ChainID       string `json:"chain_id"`
	TransactionID string `json:"transaction_id"`
	BlockID       string `json:"block_id,omitempty"`
	Slot          *int64 `json:"slot,omitempty"`
	Status        string `json:"status"`
	FeeAtomic     string `json:"fee_atomic,omitempty"`
	FeePayer      string `json:"fee_payer,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
}
type chainTransferSchema struct {
	Index                 int    `json:"index"`
	Type                  string `json:"type"`
	Asset                 string `json:"asset,omitempty"`
	Mint                  string `json:"mint,omitempty"`
	From                  string `json:"from,omitempty"`
	To                    string `json:"to,omitempty"`
	AmountAtomic          string `json:"amount_atomic"`
	Decimals              int    `json:"decimals"`
	InstructionIndex      int    `json:"instruction_index,omitempty"`
	InnerInstructionIndex int    `json:"inner_instruction_index,omitempty"`
}
type paymentListOutput struct {
	Body struct {
		Payments    []*payment `json:"payments"`
		NextTo      string     `json:"next_to,omitempty"`
		NextBlockID string     `json:"next_block_id,omitempty"`
	}
}
type paymentWebSocketOutput struct {
	Body struct {
		Description string `json:"description"`
	}
}
type paymentListInput struct {
	ConversationID     string `path:"conversationID" maxLength:"128"`
	To                 string `query:"to" doc:"latest or a payment ID; the to anchor is inclusive"`
	LastKnownPaymentID string `query:"last_known_payment_id" doc:"exclusive payment cursor"`
	LastBlockID        string `query:"last_block_id" doc:"exclusive block cursor; use next_block_id from the previous page"`
	Limit              int    `query:"limit" minimum:"1" maximum:"100"`
}
type paymentWebSocketInput struct {
	ConversationID string `path:"conversationID" maxLength:"128"`
}
type paymentGetInput struct {
	PaymentID string `path:"paymentID" maxLength:"128"`
}
type timer struct {
	Key             string `json:"key"`
	DurationSeconds int    `json:"duration_seconds" minimum:"0"`
	Enabled         bool   `json:"enabled"`
}
type timerListOutput struct {
	Body struct {
		Timers []*timer `json:"timers"`
	}
}
type timerOutput struct{ Body timer }
type timerGetInput struct {
	TimerKey string `path:"timerKey" maxLength:"128"`
}
type disappearingInput struct {
	ConversationID string `path:"conversationID" maxLength:"128"`
}
type disappearingUpdateInput struct {
	ConversationID string `path:"conversationID" maxLength:"128"`
	Body           struct {
		DurationSeconds *int  `json:"duration_seconds,omitempty" minimum:"0" maximum:"2592000"`
		Enabled         *bool `json:"enabled,omitempty"`
	}
}
type disappearingSetting struct {
	ConversationID  string `json:"conversation_id"`
	UserID          string `json:"user_id" format:"uuid"`
	DurationSeconds int    `json:"duration_seconds" minimum:"0"`
	Enabled         bool   `json:"enabled"`
	UpdatedAt       string `json:"updated_at,omitempty" format:"date-time"`
}
type disappearingOutput struct{ Body disappearingSetting }
type disappearingWebSocketOutput struct {
	Body struct {
		Description string `json:"description"`
	}
}
type paymentEventInput struct {
	Signature string `header:"X-PingPay-Signature" required:"true"`
	Body      struct {
		EventID       string `json:"event_id"`
		PaymentID     string `json:"payment_id"`
		ChainID       string `json:"chain_id"`
		TransactionID string `json:"transaction_id"`
		Status        string `json:"status"`
		BlockID       string `json:"block_id,omitempty"`
		AmountAtomic  string `json:"amount_atomic,omitempty"`
		Version       int64  `json:"version"`
		OccurredAt    string `json:"occurred_at" format:"date-time"`
	}
}
type paymentEventOutput struct {
	Body struct {
		Payment payment `json:"payment"`
	}
}

func newDocument() (*DocumentModel, error) {
	config := huma.DefaultConfig("PingMessenger API", "0.11.0")
	// Disable all Huma HTTP endpoints. The running FastHTTP router is unchanged.
	config.OpenAPIPath = ""
	config.DocsPath = ""
	config.SchemasPath = ""
	api := huma.NewAPI(config, schemaAdapter{})
	api.OpenAPI().Info.Description = "Schema-only API contract. Runtime routes are implemented by FastHTTP."
	api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearerAuth":        {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
		"openIMServiceAuth": {Type: "apiKey", In: "header", Name: "X-OpenIM-Service-Token"},
	}

	public := []map[string][]string{{}}
	bearer := []map[string][]string{{"bearerAuth": {}}}
	register[signupInput, signupOutput](api, operation(http.MethodPost, "/v1/auth/signup", "signUp", "Auth", "Create an account. Username is optional and unique when supplied; signup returns device-bound tokens.", 201, public))
	register[usernameInput, usernameOutput](api, operation(http.MethodPost, "/v1/auth/verify-username", "verifyUsername", "Auth", "Check whether a username is available.", 200, public))
	register[struct{}, usernameAvailabilityWSOutput](api, operation(http.MethodGet, "/v1/auth/username/availability", "usernameAvailabilityWebSocket", "Auth", "Upgrade to a WebSocket and send check_username JSON frames for availability results.", 101, public))
	register[passwordLoginInput, passwordLoginOutput](api, operation(http.MethodPost, "/v1/auth/login", "passwordLogin", "Auth", "Create a device session using exactly one of username, email, or ping_id plus a password. Email login is allowed only after the email contact is OTP-verified; email is optional at signup.", 200, public))
	register[loginStartInput, loginStartOutput](api, operation(http.MethodPost, "/v1/auth/login/start", "startLogin", "Auth", "Start passwordless email login.", 202, public))
	register[loginVerifyInput, loginVerifyOutput](api, operation(http.MethodPost, "/v1/auth/login/verify", "verifyLogin", "Auth", "Verify a passwordless login code and create a session.", 200, public))
	register[refreshInput, refreshOutput](api, operation(http.MethodPost, "/v1/auth/refresh", "refreshSession", "Auth", "Rotate a refresh token. A successful rotation atomically replaces and invalidates the presented refresh token. This operation has no idempotency key, replay grace, or concurrent-refresh guarantee. Clients must serialize refresh attempts and durably persist the returned replacement credentials before treating rotation as committed; replaying the old token after a lost response is not guaranteed to recover the replacement credentials.", 200, public))
	register[passwordRecoveryStartInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/recovery/start", "startPasswordRecovery", "Auth", "Start privacy-preserving password recovery by username, ping ID, email, or phone.", 202, public))
	register[passwordChallengeInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/recovery/complete", "completePasswordRecovery", "Auth", "Complete password recovery with a one-time code.", 200, public))
	register[passwordChangeInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/change", "changePassword", "Auth", "Change a password in one step. The current session remains signed in and all other sessions are revoked; verified contacts do not add an OTP step.", 200, bearer))
	register[passwordChangeVerifyInput, passwordStatusOutput](api, operation(http.MethodPost, "/v1/auth/password/change/verify", "verifyPasswordChange", "Auth", "Complete contact step-up for a password change.", 200, bearer))
	challengeOperation := operation(http.MethodPost, "/v1/mfa/challenge", "issueMFAChallenge", "MFA", "Issue an email or phone verification challenge. Requests are limited to one per minute and five per hour per contact and requester IP; throttled requests return 429 with Retry-After.", 202, public)
	challengeOperation.Errors = append(challengeOperation.Errors, http.StatusTooManyRequests)
	register[challengeInput, challengeOutput](api, challengeOperation)
	register[verifyInput, verifyOutput](api, operation(http.MethodPost, "/v1/mfa/verify", "verifyMFAChallenge", "MFA", "Verify a non-login MFA challenge.", 200, public))
	addContact := operation(http.MethodPost, "/v1/mfa/contacts", "addMFAContact", "MFA", "Add an email or phone contact after signup. The contact remains unverified until the returned challenge is completed; a verified contact cannot be changed.", 202, bearer)
	addContact.Errors = append(addContact.Errors, http.StatusConflict, http.StatusTooManyRequests)
	register[addContactInput, addContactOutput](api, addContact)
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
	register[struct{}, profileImageDeleteOutput](api, operation(http.MethodDelete, "/v1/profile/image", "deleteProfileImage", "Profile", "Remove the authenticated user's profile image and clear its OpenIM faceURL.", 200, bearer))
	register[usernameInput, usernameUpdateOutput](api, operation(http.MethodPatch, "/v1/profile/username", "updateUsername", "Profile", "Set or change the optional unique username.", 200, bearer))
	profileUpdate := operation(http.MethodPost, "/v1/profile/update", "updateProfile", "Profile", "Update a non-unique nickname and/or upload an image as multipart/form-data (JPEG, PNG, or WebP; max 5 MiB).", 200, bearer)
	profileUpdate.MaxBodyBytes = 5 << 20
	register[profileUpdateInput, profileUpdateOutput](api, profileUpdate)
	storyCreate := operation(http.MethodPost, "/v1/stories", "createStory", "Stories", "Create an image, video, or text status. Text statuses carry up to 700 characters and a required #RRGGBB background color; media responses use short-lived private S3 URLs. Idempotency-Key is optional and owner-scoped: the same key and semantic payload returns the original story for 30 days; a different payload returns 409, and a deleted or expired result returns 410. Requests without a key retain legacy behavior and may create duplicates when retried after an unknown outcome.", 201, bearer)
	storyCreate.MaxBodyBytes = 10 << 20
	storyCreate.Errors = append(storyCreate.Errors, http.StatusConflict, http.StatusGone, http.StatusTooManyRequests)
	register[storyCreateInput, storyCreateOutput](api, storyCreate)
	register[storyDeleteInput, storyDeleteOutput](api, operation(http.MethodDelete, "/v1/stories/{storyID}", "deleteStory", "Stories", "Delete one of the authenticated user's stories.", 200, bearer))
	storyMediaDownload := operation(http.MethodGet, "/v1/stories/{storyID}/media", "downloadStoryMedia", "Stories", "Stream the complete private story media object as the raw response body (not JSON or a redirect). The proxy URI has no signed-URL TTL; each request requires a valid signed-in bearer session and current recipient authorization. Persist story IDs, not event media_url values: those are temporary private S3 presigned bearer capabilities valid for 10 minutes and are not durable job credentials. No long-lived or signed-job media URL route is offered; a background worker must use this authenticated proxy only with a valid session. Missing, expired, deleted, and non-recipient stories return 404. Content-Type is the stored object MIME type (image/* or video/*). The proxy does not implement byte ranges or conditional requests and does not set Cache-Control or other cache validators. When Redis is configured, limits are 120 requests per authenticated user and 600 per peer IP per minute; otherwise the process-local development limiter is used.", 200, bearer)
	storyMediaDownload.Errors = append(storyMediaDownload.Errors, http.StatusNotFound, http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable)
	storyMediaDownload.Responses = map[string]*huma.Response{
		"200": {
			Description: "Complete story media bytes. The server streams the full object and sets Content-Length from the stored byte size.",
			Content: map[string]*huma.MediaType{
				"image/*":                  {Schema: &huma.Schema{Type: "string", Format: "binary"}},
				"video/*":                  {Schema: &huma.Schema{Type: "string", Format: "binary"}},
				"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
			},
		},
	}
	register[storyDeleteInput, struct{}](api, storyMediaDownload)
	register[storyViewInput, storyViewOutput](api, operation(http.MethodPost, "/v1/stories/{storyID}/view", "viewStory", "Stories", "Upsert the current view and optional reaction for this story/viewer pair. The unique pair prevents duplicate rows, but each call refreshes viewed_at and replaces reaction (an omitted/empty reaction clears it); repeating a request is not idempotent as a state update.", 200, bearer))
	register[storyViewsInput, storyViewsOutput](api, operation(http.MethodGet, "/v1/stories/{storyID}/views", "listStoryViews", "Stories", "List views and reactions for the authenticated owner's story.", 200, bearer))
	register[struct{}, storyWebSocketOutput](api, operation(http.MethodGet, "/v1/stories/ws", "storyWebSocket", "Stories", "Upgrade to the authenticated, default-subscribed story WebSocket (maximum 8 concurrent connections per user). Send JSON {type:sync|get,global_cursor,user_cursors,limit} to replay events; batches contain events, updated cursors, and has_more (default page size 100, maximum 200). Persist a batch before sending {type:ack,global_cursor,user_cursors}; cursors cannot move backwards. Live stories.event messages carry a created or deleted event and global_cursor. Created media_url values are short-lived private S3 URLs; use authenticated GET /v1/stories/{storyID}/media for durable downloads. See docs/story-sync-contract.md for message shapes, cursor recovery, and media behavior.", 101, bearer))
	documentUpload := operation(http.MethodPost, "/v1/documents", "uploadDocument", "Documents", "Upload a document for a normal OpenIM contentType=105 file message. Optionally send Idempotency-Key and reuse that key with the exact same filename, MIME type, and bytes after an ambiguous response; a replay returns the original document with 200, without reserving quota or creating a second object. Reusing the key for different content returns 409; replay after deletion or expiry returns 410. Keys remain bound to document tombstones. See docs/api/document-upload-idempotency.md for the full recovery contract. The response includes a retention-bound signed sourceUrl and fileElem metadata. Default policy is 100 MiB per file, 20 retained documents, 1 GiB per rolling 24 hours, and seven-day retention.", 201, bearer)
	if documentUpload.Responses == nil {
		documentUpload.Responses = map[string]*huma.Response{}
	}
	documentUpload.Responses["200"] = &huma.Response{
		Description: "Idempotent replay of an existing document upload; response body matches 201.",
		Content: map[string]*huma.MediaType{
			"application/json": {Schema: &huma.Schema{Ref: "#/components/schemas/DocumentUploadOutputBody"}},
		},
	}
	documentUpload.Errors = append(documentUpload.Errors, http.StatusConflict, http.StatusGone, http.StatusTooManyRequests, http.StatusUnsupportedMediaType, http.StatusRequestEntityTooLarge)
	documentUpload.MaxBodyBytes = 101 << 20
	register[documentUploadInput, documentUploadOutput](api, documentUpload)
	register[documentPathInput, documentGetOutput](api, operation(http.MethodGet, "/v1/documents/{documentID}", "getDocument", "Documents", "Get document metadata and a fresh retention-bound sourceUrl while the object is retained.", 200, bearer))
	register[documentPathInput, struct{}](api, operation(http.MethodGet, "/v1/documents/{documentID}/download", "downloadDocument", "Documents", "Download a document through the authenticated, rate-limited platform proxy.", 200, bearer))
	register[documentPathInput, struct{}](api, operation(http.MethodDelete, "/v1/documents/{documentID}", "deleteDocument", "Documents", "Delete the S3 object while retaining a tombstone for message history. Expired objects are removed automatically after seven days.", 204, bearer))
	register[documentMessageLinkInput, struct{}](api, operation(http.MethodPost, "/v1/documents/{documentID}/message-link", "linkDocumentMessage", "Documents", "Link an uploaded document to its OpenIM conversation sequence so disappearing-message cleanup can revoke the message before deleting S3 media.", 201, bearer))
	discover := operation(http.MethodPost, "/v1/friends/discover-network", "discoverNetwork", "Friends", "Discover other active accounts from verified contacts matching the supplied email addresses or E.164 phone numbers. Each array is optional and accepts up to 100 values; at least one non-empty array is required. Emails are trimmed and lowercased; phone numbers are trimmed but otherwise matched exactly. Returns distinct OpenIM user IDs (not Platform account UUIDs); no matches return null. Requires an active bearer session. Invalid input returns 422, inactive/missing authentication returns 401, and database failures return 500. See docs/friend-contact-discovery.md for the full request and matching contract.", 200, bearer)
	// This endpoint is not rate limited in the FastHTTP handler. The common
	// operation helper includes 429 for routes that use that policy, so remove
	// that generic response here to keep the contract faithful to runtime.
	discover.Errors = []int{http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusInternalServerError}
	register[discoverInput, discoverOutput](api, discover)
	register[struct{}, friendsOutput](api, operation(http.MethodGet, "/v1/friends", "listFriends", "Friends", "List the authenticated user's OpenIM friends with public platform profile fields. Each result includes both user_id (Platform account UUID) and openim_user_id (OpenIM identity); clients can map between them without assuming the values match. The story viewed-by endpoint is GET /v1/stories/{storyID}/views.", 200, bearer))
	register[friendSearchInput, friendSearchOutput](api, operation(http.MethodPost, "/v1/friends/search", "searchFriends", "Friends", "Search active users by partial username or exact verified email, phone, or PingID. Results use OpenIM-compatible identity fields; relationship is -1 because this global directory lookup does not evaluate the local friend graph.", 200, bearer))
	register[struct{}, blocksOutput](api, operation(http.MethodGet, "/v1/blocks", "listBlocks", "Blocks", "List only users blocked by the authenticated user; OpenIM is the source of truth.", 200, bearer))
	register[blockInput, blockCreatedOutput](api, operation(http.MethodPost, "/v1/blocks", "addBlock", "Blocks", "Block a user by PingID and synchronize the OpenIM blacklist.", 201, bearer))
	register[blockPathInput, emptyOutput](api, operation(http.MethodDelete, "/v1/blocks/{pingID}", "removeBlock", "Blocks", "Unblock a user by PingID and synchronize the OpenIM blacklist.", 204, bearer))
	register[accountDeleteInput, accountDeleteOutput](api, operation(http.MethodPost, "/v1/account/delete", "deleteAccount", "Account", "Soft-delete the account after password confirmation and accept_terms=true. Sessions are revoked and the account cannot log in again; application, OpenIM, media, and payment data are retained for audit/retention. PingID, username, and linked contact identifiers remain reserved and cannot be reused.", 200, bearer))
	register[paymentCreateInput, paymentCreateOutput](api, operation(http.MethodPost, "/v1/conversations/{conversationID}/payments", "createPayment", "Payments", "Create an idempotent payment ledger record for a conversation.", 201, bearer))
	register[paymentListInput, paymentListOutput](api, operation(http.MethodGet, "/v1/conversations/{conversationID}/payments", "listPayments", "Payments", "List visible payments newest-first. Use to=latest or a payment ID as the inclusive anchor, then last_known_payment_id and/or last_block_id as exclusive cursors; next_to and next_block_id continue the page.", 200, bearer))
	register[paymentListInput, paymentListOutput](api, operation(http.MethodGet, "/v1/conversations/{conversationID}/payments/sync", "syncPayments", "Payments", "Synchronize visible payment projections with the same reverse-time cursor semantics; repeat-safe HTTP recovery source for the payment WebSocket.", 200, bearer))
	register[paymentWebSocketInput, paymentWebSocketOutput](api, operation(http.MethodGet, "/v1/conversations/{conversationID}/payments/ws", "paymentWebSocket", "Payments", "Upgrade to a participant-filtered WebSocket. Both from_user_id and to_user_id receive payment.updated events, including chain success, chain failure, and platform timeout failures; HTTP remains the recovery source of truth.", 101, bearer))
	register[paymentGetInput, paymentGetOutput](api, operation(http.MethodGet, "/v1/payments/{paymentID}", "getPayment", "Payments", "Get one payment visible to the authenticated participant.", 200, bearer))
	register[paymentGetInput, paymentChainOutput](api, operation(http.MethodGet, "/v1/payments/{paymentID}/chain", "getPaymentChain", "Payments", "Get the normalized transaction and native/token transfers extracted from the chain RPC. The response is unavailable until indexing succeeds.", 200, bearer))
	register[paymentEventInput, paymentEventOutput](api, operation(http.MethodPost, "/v1/internal/payments/events", "applyPaymentEvent", "Internal", "Apply a signed PingPay-user transaction update to the platform ledger.", 200, public))
	register[struct{}, timerListOutput](api, operation(http.MethodGet, "/v1/settings/timers", "listGlobalTimers", "Settings", "List global timer values for authenticated clients; timers are operator-controlled.", 200, bearer))
	register[timerGetInput, timerOutput](api, operation(http.MethodGet, "/v1/settings/timers/{timerKey}", "getGlobalTimer", "Settings", "Get one operator-controlled global timer.", 200, bearer))
	register[disappearingInput, disappearingOutput](api, operation(http.MethodGet, "/v1/conversations/{conversationID}/disappearing", "getDisappearingSetting", "Messages", "Get the authenticated user's disappearing-message setting for a conversation.", 200, bearer))
	register[disappearingUpdateInput, disappearingOutput](api, operation(http.MethodPut, "/v1/conversations/{conversationID}/disappearing", "setDisappearingSetting", "Messages", "Set or disable disappearing messages for a conversation.", 200, bearer))
	register[disappearingInput, emptyOutput](api, operation(http.MethodDelete, "/v1/conversations/{conversationID}/disappearing", "deleteDisappearingSetting", "Messages", "Disable disappearing messages for a conversation.", 204, bearer))
	register[disappearingInput, disappearingWebSocketOutput](api, operation(http.MethodGet, "/v1/conversations/{conversationID}/disappearing/ws", "disappearingWebSocket", "Messages", "Receive disappearing-message setting notifications for a conversation.", 101, bearer))
	register[struct{}, notificationPreferencesOutput](api, operation(http.MethodGet, "/v1/settings/notifications", "getNotificationPreferences", "Settings", "Get account-wide chat notification preferences; absent settings default to enabled/all/default sound/2-second grouping. For push delivery, enabled=false and mode=none suppress alerts; mentions_only allows only recipient mentions (including @all). OpenIM treats sound as a presentation preference and the frequency as a minimum interval between updates for a recipient and conversation; it sends the first eligible alert immediately and coalesces later updates to the latest preview. Actual sound remains subject to native OS channel settings.", 200, bearer))
	register[notificationPreferencesInput, notificationPreferencesOutput](api, operation(http.MethodPut, "/v1/settings/notifications", "putNotificationPreferences", "Settings", "Atomically update supplied account-wide chat notification fields; provide at least one field. Valid modes are all, mentions_only, none; sound is default or silent; update frequency is 2, 5, or 10 seconds. OpenIM applies these preferences to push delivery; the preference API itself does not send or coalesce notifications. Actual sound remains subject to native OS channel settings.", 200, bearer))
	getConversationNotifications := operation(http.MethodGet, "/v1/conversations/{conversationID}/notifications", "getConversationNotificationPreferences", "Settings", "Get the effective account and per-conversation notification settings.", 200, bearer)
	getConversationNotifications.Errors = append(getConversationNotifications.Errors, http.StatusBadRequest)
	register[notificationConversationPathInput, notificationPreferencesOutput](api, getConversationNotifications)
	putConversationNotifications := operation(http.MethodPut, "/v1/conversations/{conversationID}/notifications", "putConversationNotificationPreferences", "Settings", "Atomically update supplied per-conversation overrides; provide at least one field. Omitted fields retain existing overrides and deleting the override restores account defaults.", 200, bearer)
	putConversationNotifications.Errors = append(putConversationNotifications.Errors, http.StatusBadRequest)
	register[notificationConversationInput, notificationPreferencesOutput](api, putConversationNotifications)
	deleteConversationNotifications := operation(http.MethodDelete, "/v1/conversations/{conversationID}/notifications", "deleteConversationNotificationPreferences", "Settings", "Remove all per-conversation overrides without a request body and inherit account-wide notification settings.", 204, bearer)
	deleteConversationNotifications.Errors = append(deleteConversationNotifications.Errors, http.StatusBadRequest)
	register[notificationConversationPathInput, emptyOutput](api, deleteConversationNotifications)
	resolveNotification := operation(http.MethodPost, "/v1/internal/notifications/resolve", "resolveNotificationPreferences", "Internal", "Resolve active Platform user preferences for OpenIM push delivery. The response preferences map may omit unknown, inactive, or deleted OpenIM IDs; omission is a successful partial result, not an outage. An unset Platform service token returns 503, an invalid token 401, invalid input 422, and a storage/query failure 500. This endpoint trusts the authenticated OpenIM service to supply the conversation and recipient set. Resolver outage policy (OpenIM fail-open or fail-closed) must be explicitly configured by deployment and is not selected by Platform.", 200, []map[string][]string{{"openIMServiceAuth": {}}})
	resolveNotification.Errors = append(resolveNotification.Errors, http.StatusServiceUnavailable)
	register[notificationResolveInput, notificationResolveOutput](api, resolveNotification)

	document := api.OpenAPI()
	if discover := document.Components.Schemas.Map()["DiscoverBody"]; discover != nil {
		// Huma's field tags describe per-field constraints. The runtime also
		// requires at least one non-empty contact list, which is a relationship
		// between optional fields and therefore represented as JSON Schema anyOf.
		one := 1
		discover.AnyOf = []*huma.Schema{
			{Type: "object", Required: []string{"emails"}, Properties: map[string]*huma.Schema{
				"emails": {Type: "array", MinItems: &one},
			}},
			{Type: "object", Required: []string{"mobile_no"}, Properties: map[string]*huma.Schema{
				"mobile_no": {Type: "array", MinItems: &one},
			}},
		}
		if emails := discover.Properties["emails"]; emails != nil && emails.Items != nil {
			emails.Items.Format = "email"
		}
		if phones := discover.Properties["mobile_no"]; phones != nil && phones.Items != nil {
			maxLength := 32
			phones.Items.MaxLength = &maxLength
		}
	}
	if notificationPreferences := document.Components.Schemas.Map()["NotificationPreferencesBody"]; notificationPreferences != nil {
		// Runtime partial-update validation rejects an empty object, while each
		// individual preference remains optional. Represent that cross-field
		// rule in the request schema.
		one := 1
		notificationPreferences.MinProperties = &one
		notificationPreferences.AnyOf = make([]*huma.Schema, 0, 4)
		for _, field := range []string{"enabled", "mode", "sound", "max_update_frequency_seconds"} {
			notificationPreferences.AnyOf = append(notificationPreferences.AnyOf, &huma.Schema{
				Type:     "object",
				Required: []string{field},
			})
		}
	}
	if notificationResolve := document.Components.Schemas.Map()["NotificationResolveBody"]; notificationResolve != nil {
		if ids := notificationResolve.Properties["openim_user_ids"]; ids != nil && ids.Items != nil {
			// The resolver requires a non-empty array and rejects JSON null at
			// runtime. Go's slice schema is nullable by default, so make that
			// contract explicit in the generated OpenAPI document.
			ids.Nullable = false
			maxLength := 128
			ids.Items.MaxLength = &maxLength
		}
	}
	return document, nil
}

type notificationPreferencesBody struct {
	Enabled                   *bool   `json:"enabled,omitempty"`
	Mode                      *string `json:"mode,omitempty" enum:"all,mentions_only,none"`
	Sound                     *string `json:"sound,omitempty" enum:"default,silent"`
	MaxUpdateFrequencySeconds *int    `json:"max_update_frequency_seconds,omitempty" enum:"2,5,10"`
}
type notificationPreferencesResult struct {
	Enabled                   bool   `json:"enabled"`
	Mode                      string `json:"mode" enum:"all,mentions_only,none"`
	Sound                     string `json:"sound" enum:"default,silent"`
	MaxUpdateFrequencySeconds int    `json:"max_update_frequency_seconds" enum:"2,5,10"`
}
type notificationPreferencesInput struct{ Body notificationPreferencesBody }
type notificationPreferencesOutput struct{ Body notificationPreferencesResult }
type notificationConversationPathInput struct {
	ConversationID string `path:"conversationID" maxLength:"256"`
}
type notificationConversationInput struct {
	ConversationID string `path:"conversationID" maxLength:"256"`
	Body           notificationPreferencesBody
}
type notificationResolveBody struct {
	OpenIMUserIDs  []string `json:"openim_user_ids" minItems:"1" maxItems:"1000"`
	ConversationID string   `json:"conversation_id" minLength:"1" maxLength:"256"`
}
type notificationResolveInput struct{ Body notificationResolveBody }
type notificationResolveOutput struct {
	Body struct {
		Preferences map[string]notificationPreferencesResult `json:"preferences"`
	}
}

func register[I, O any](api huma.API, op huma.Operation) {
	huma.Register(api, op, func(context.Context, *I) (*O, error) { return nil, nil })
}

func operation(method, path, id, tag, description string, status int, security []map[string][]string) huma.Operation {
	return huma.Operation{
		Method: method, Path: path, OperationID: id, Tags: []string{tag}, Description: description,
		DefaultStatus: status, Errors: []int{http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusInternalServerError}, Security: security,
	}
}
