// Package openapi produces the platform's OpenAPI document without owning HTTP
// transport. FastHTTP remains the runtime router; Huma is used only for its
// operation and JSON-schema generation.
package openapi

// Document returns an OpenAPI 3.1 document for the FastHTTP endpoints.
func Document() (*DocumentModel, error) {
	return newDocument()
}

// DocumentModel deliberately keeps the Huma dependency behind this package's
// public API. This alias lets the generator marshal Huma's document directly.
// It is declared in huma.go so consumers do not need to know implementation
// details when adding route descriptions.
