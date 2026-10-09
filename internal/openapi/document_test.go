package openapi

import "testing"

func TestDocumentCoversRuntimeRoutes(t *testing.T) {
	doc, err := Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q", doc.OpenAPI)
	}
	if doc.Info.Version != "0.5.0" {
		t.Fatalf("API version = %q", doc.Info.Version)
	}
	for _, path := range []string{
		"/v1/auth/signup", "/v1/auth/verify-username", "/v1/auth/login", "/v1/auth/login/start", "/v1/auth/login/verify", "/v1/auth/refresh",
		"/v1/mfa/challenge", "/v1/mfa/verify", "/v1/security/backup-codes", "/v1/security/backup-codes/regenerate",
		"/v1/security/activity", "/v1/security/revoke", "/v1/profile/", "/v1/profile/update", "/v1/friends/discover-network",
	} {
		if doc.Paths[path] == nil {
			t.Errorf("missing documented path %s", path)
		}
	}
	if doc.Components.SecuritySchemes["bearerAuth"] == nil {
		t.Fatal("missing bearerAuth scheme")
	}
	profile := doc.Paths["/v1/profile/update"].Post.RequestBody.Content["multipart/form-data"]
	if profile == nil || profile.Schema == nil || profile.Schema.Properties["image"] == nil {
		t.Fatal("profile upload must be documented as a multipart image field")
	}
	if profile.Schema.Properties["nickname"] == nil {
		t.Fatal("profile update must document the nickname field")
	}
	if doc.Paths["/openapi.json"] != nil || doc.Paths["/docs"] != nil {
		t.Fatal("schema-only generation must not add Huma HTTP routes")
	}
}
