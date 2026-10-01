.PHONY: migrate seed test vet e2e openapi

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

openapi:
	go run ./cmd/openapi -output openapi/openapi.json
