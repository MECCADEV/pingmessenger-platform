.PHONY: migrate seed test vet e2e e2e-document openapi

migrate:
	go run ./cmd/db -mode=migrate

seed:
	go run ./cmd/db -mode=seed

test:
	go test ./...

vet:
	go vet ./...

e2e:
	go test -tags=e2e ./tests/e2e

# Runs the authenticated multipart upload plus contentType=105 WebSocket
# delivery check. Set E2E_PRODUCTION=1 and E2E_BASE_URL before invoking it.
e2e-document:
	go test -tags=e2e ./tests/e2e -run '^TestProductionOpenIMMultipartDocumentUpload$$' -count=1 -v

openapi:
	go run ./cmd/openapi -output openapi/openapi.json
