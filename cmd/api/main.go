package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/config"
	"pingmessenger/internal/httpapi"
	"pingmessenger/internal/notify"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		log.Fatal(err)
	}
	openIM, err := openim.NewHTTPClient(cfg.OpenIMAPIBaseURL, cfg.OpenIMAPIToken)
	if err != nil {
		log.Fatal(err)
	}
	defer openIM.Close()
	email, err := notify.New(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	tokens := auth.NewTokenManager(cfg.JWTSigningSecret, cfg.JWTIssuer, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	objects, err := storage.NewS3(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	server := &fasthttp.Server{Handler: httpapi.New(pool, openIM, email, cfg.JWTSigningSecret, tokens, objects, cfg.S3PublicBaseURL).Router(), Name: "pingmessenger-api"}
	log.Printf("PingMessenger API listening on %s (%s)", cfg.HTTPAddr, cfg.AppEnv)
	if err := server.ListenAndServe(cfg.HTTPAddr); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
