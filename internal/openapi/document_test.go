package openapi

import (
	"strings"
	"testing"
)

func TestDocumentCoversRuntimeRoutes(t *testing.T) {
	doc, err := Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q", doc.OpenAPI)
	}
	if doc.Info.Version != "0.11.0" {
		t.Fatalf("API version = %q", doc.Info.Version)
	}
	for _, path := range []string{
		"/v1/auth/signup", "/v1/auth/verify-username", "/v1/auth/login", "/v1/auth/login/start", "/v1/auth/login/verify", "/v1/auth/refresh",
		"/v1/mfa/challenge", "/v1/mfa/verify", "/v1/security/backup-codes", "/v1/security/backup-codes/regenerate",
		"/v1/mfa/contacts",
		"/v1/security/activity", "/v1/security/revoke", "/v1/profile/", "/v1/profile/update", "/v1/profile/image", "/v1/friends", "/v1/friends/discover-network", "/v1/friends/search", "/v1/blocks", "/v1/blocks/{pingID}", "/v1/account/delete",
		"/v1/conversations/{conversationID}/payments", "/v1/conversations/{conversationID}/payments/ws",
		"/v1/payments/{paymentID}/chain",
		"/v1/stories", "/v1/stories/{storyID}", "/v1/stories/{storyID}/view", "/v1/stories/{storyID}/views", "/v1/stories/ws",
		"/v1/documents", "/v1/documents/{documentID}",
	} {
		if doc.Paths[path] == nil {
			t.Errorf("missing documented path %s", path)
		}
	}
	if doc.Components.SecuritySchemes["bearerAuth"] == nil {
		t.Fatal("missing bearerAuth scheme")
	}
	if doc.Components.SecuritySchemes["openIMServiceAuth"] == nil || doc.Components.SecuritySchemes["openIMServiceAuth"].Name != "X-OpenIM-Service-Token" {
		t.Fatal("missing dedicated OpenIM notification resolver auth scheme")
	}
	discovery := doc.Paths["/v1/friends/discover-network"].Post
	if len(discovery.Security) != 1 || discovery.Security[0]["bearerAuth"] == nil {
		t.Fatal("contact discovery must require bearer authentication")
	}
	for _, status := range []string{"200", "401", "422", "500"} {
		if discovery.Responses[status] == nil {
			t.Errorf("contact discovery must document response %s", status)
		}
	}
	if discovery.Responses["429"] != nil {
		t.Fatal("contact discovery must not claim a 429 response without a runtime rate limit")
	}
	if !strings.Contains(discovery.Description, "at least one non-empty array") || !strings.Contains(discovery.Description, "OpenIM user IDs") || !strings.Contains(discovery.Description, "docs/friend-contact-discovery.md") {
		t.Fatalf("contact discovery description is missing contract details: %q", discovery.Description)
	}
	discoveryBody := doc.Components.Schemas.Map()["DiscoverBody"]
	if discoveryBody == nil {
		t.Fatal("contact discovery request schema is missing")
	}
	emails := discoveryBody.Properties["emails"]
	if emails == nil || emails.MaxItems == nil || *emails.MaxItems != 100 || emails.Items == nil || emails.Items.Format != "email" {
		t.Fatal("contact discovery email list must document email format and the 100-item cap")
	}
	phones := discoveryBody.Properties["mobile_no"]
	if phones == nil || phones.MaxItems == nil || *phones.MaxItems != 100 || phones.Items == nil || phones.Items.MaxLength == nil || *phones.Items.MaxLength != 32 {
		t.Fatal("contact discovery phone list must document the 100-item cap and 32-character item limit")
	}
	if len(discoveryBody.AnyOf) != 2 {
		t.Fatal("contact discovery request schema must require at least one non-empty contact list")
	}
	for _, alternative := range discoveryBody.AnyOf {
		if len(alternative.Required) != 1 || len(alternative.Properties) != 1 {
			t.Fatalf("contact discovery anyOf branch must require one contact list: %#v", alternative)
		}
		for field, constraint := range alternative.Properties {
			if field != "emails" && field != "mobile_no" {
				t.Errorf("unexpected contact discovery anyOf field %q", field)
			}
			if constraint.MinItems == nil || *constraint.MinItems != 1 {
				t.Errorf("contact discovery %s alternative must require a non-empty array", field)
			}
		}
	}
	for _, path := range []string{
		"/v1/settings/notifications",
		"/v1/conversations/{conversationID}/notifications",
		"/v1/internal/notifications/resolve",
	} {
		if doc.Paths[path] == nil {
			t.Errorf("missing documented notification preference path %s", path)
		}
	}
	resolver := doc.Paths["/v1/internal/notifications/resolve"].Post
	if len(resolver.Security) != 1 || resolver.Security[0]["openIMServiceAuth"] == nil {
		t.Fatal("notification resolver must document dedicated service authentication")
	}
	for _, term := range []string{"successful partial result", "503", "storage/query failure 500", "fail-open or fail-closed"} {
		if !strings.Contains(resolver.Description, term) {
			t.Errorf("notification resolver description missing %q: %q", term, resolver.Description)
		}
	}
	settings := doc.Paths["/v1/settings/notifications"]
	for _, description := range []string{settings.Get.Description, settings.Put.Description} {
		for _, term := range []string{"mentions_only", "native OS channel settings"} {
			if !strings.Contains(description, term) {
				t.Errorf("notification preference description missing %q: %q", term, description)
			}
		}
	}
	profile := doc.Paths["/v1/profile/update"].Post.RequestBody.Content["multipart/form-data"]
	if profile == nil || profile.Schema == nil || profile.Schema.Properties["image"] == nil {
		t.Fatal("profile upload must be documented as a multipart image field")
	}
	if profile.Schema.Properties["nickname"] == nil {
		t.Fatal("profile update must document the nickname field")
	}
	storyCreate := doc.Paths["/v1/stories"].Post
	if !strings.Contains(storyCreate.Description, "Idempotency-Key") || !strings.Contains(storyCreate.Description, "30 days") || !strings.Contains(storyCreate.Description, "409") || !strings.Contains(storyCreate.Description, "410") {
		t.Fatal("story create contract must document idempotency, retention, and terminal replay responses")
	}
	var hasIdempotencyHeader bool
	for _, parameter := range storyCreate.Parameters {
		if parameter.In == "header" && parameter.Name == "Idempotency-Key" && !parameter.Required {
			hasIdempotencyHeader = true
		}
	}
	if !hasIdempotencyHeader {
		t.Fatal("story create must expose an optional Idempotency-Key header")
	}
	storySync := doc.Paths["/v1/stories/ws"].Get
	for _, term := range []string{"type:sync|get", "has_more", "type:ack", "cursors cannot move backwards", "maximum 200", "/v1/stories/{storyID}/media", "docs/story-sync-contract.md"} {
		if !strings.Contains(storySync.Description, term) {
			t.Errorf("story sync WebSocket documentation missing %q: %q", term, storySync.Description)
		}
	}
	storyForm := storyCreate.RequestBody.Content["multipart/form-data"]
	if storyForm == nil || storyForm.Schema == nil || storyForm.Schema.Properties["user_ids"] == nil {
		t.Fatal("story create must document selected audience IDs")
	}
	userIDsDescription := storyForm.Schema.Properties["user_ids"].Description
	for _, term := range []string{"Platform account UUIDs", "not OpenIM IDs", "GET /v1/friends", "openim_user_id", "user_id"} {
		if !strings.Contains(userIDsDescription, term) {
			t.Errorf("story user_ids documentation missing %q: %q", term, userIDsDescription)
		}
	}
	friendsList := doc.Paths["/v1/friends"].Get
	for _, term := range []string{"user_id", "Platform account UUID", "openim_user_id", "OpenIM identity", "without assuming the values match"} {
		if !strings.Contains(friendsList.Description, term) {
			t.Errorf("friends identity mapping documentation missing %q: %q", term, friendsList.Description)
		}
	}
	for _, status := range []string{"409", "410"} {
		if storyCreate.Responses[status] == nil {
			t.Fatalf("story create must document response %s", status)
		}
	}
	storyView := doc.Paths["/v1/stories/{storyID}/view"].Post
	if !strings.Contains(storyView.Description, "prevents duplicate rows") || !strings.Contains(storyView.Description, "not idempotent") {
		t.Fatal("story view contract must distinguish row deduplication from retry idempotency")
	}
	storyMedia := doc.Paths["/v1/stories/{storyID}/media"].Get
	if !strings.Contains(storyMedia.Description, "raw response body") || !strings.Contains(storyMedia.Description, "does not implement byte ranges") {
		t.Fatal("story media contract must describe the raw full-object response and unsupported ranges")
	}
	for _, contentType := range []string{"image/*", "video/*", "application/octet-stream"} {
		if storyMedia.Responses["200"].Content[contentType] == nil {
			t.Fatalf("story media contract must declare binary response type %q", contentType)
		}
	}
	document := doc.Paths["/v1/documents"].Post.RequestBody.Content["multipart/form-data"]
	if document == nil || document.Schema == nil || document.Schema.Properties["document"] == nil {
		t.Fatal("document upload must be documented as a multipart document field")
	}
	documentUpload := doc.Paths["/v1/documents"].Post
	for _, term := range []string{"Idempotency-Key", "exact same filename, MIME type, and bytes", "returns the original document", "tombstones"} {
		if !strings.Contains(documentUpload.Description, term) {
			t.Errorf("document upload idempotency contract missing %q: %q", term, documentUpload.Description)
		}
	}
	for _, status := range []string{"200", "201", "409", "410"} {
		if documentUpload.Responses[status] == nil {
			t.Errorf("document upload must document response %s", status)
		}
	}
	if documentUpload.Responses["200"].Content["application/json"].Schema.Ref != documentUpload.Responses["201"].Content["application/json"].Schema.Ref {
		t.Fatal("idempotent replay and first upload must share the response schema")
	}
	keyHeader := false
	for _, parameter := range documentUpload.Parameters {
		if parameter.Name == "Idempotency-Key" && parameter.In == "header" {
			keyHeader = true
			break
		}
	}
	if !keyHeader {
		t.Fatal("document upload must expose Idempotency-Key as a request header")
	}
	if doc.Paths["/openapi.json"] != nil || doc.Paths["/docs"] != nil {
		t.Fatal("schema-only generation must not add Huma HTTP routes")
	}
}
