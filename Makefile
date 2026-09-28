.PHONY: migrate seed test vet e2e

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
