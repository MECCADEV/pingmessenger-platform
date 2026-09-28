FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/db ./cmd/db

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/api /usr/local/bin/pingmessenger-api
COPY --from=build /out/db /usr/local/bin/pingmessenger-db
COPY --chown=nonroot:nonroot migrations /app/migrations
COPY --chown=nonroot:nonroot seeds /app/seeds
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/pingmessenger-api"]
