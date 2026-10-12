package main

import (
	"context"
	"log"
	"net"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"pingmessenger/internal/auth"
	"pingmessenger/internal/config"
	"pingmessenger/internal/httpapi"
	"pingmessenger/internal/notify"
	"pingmessenger/internal/openim"
	"pingmessenger/internal/storage"
	"pingmessenger/internal/webhookgrpc"
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
	openIM, err := openim.NewHTTPClientWithSharedSecretAndConversationGRPC(cfg.OpenIMAPIBaseURL, cfg.OpenIMAPIToken, cfg.OpenIMSharedSecret, cfg.OpenIMAdminUserID, cfg.OpenIMConversationGRPCTarget)
	if err != nil {
		log.Fatal(err)
	}
	defer openIM.Close()
	email, err := notify.New(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	sms, err := notify.NewDirectSMS(context.Background(), cfg.AWSRegion, cfg.SMSMirrorTopicARN)
	if err != nil {
		log.Fatal(err)
	}
	tokens := auth.NewTokenManager(cfg.JWTSigningSecret, cfg.JWTIssuer, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	deviceTokenCipher, err := auth.NewDeviceTokenCipher(cfg.DeviceTokenEncryptionKey)
	if err != nil {
		log.Fatal(err)
	}
	objects, err := storage.NewS3(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	api := httpapi.New(pool, openIM, email, sms, cfg.JWTSigningSecret, tokens, deviceTokenCipher, objects, cfg.S3PublicBaseURL, cfg.PingPayEventSecret)
	grpcListener, err := net.Listen("tcp", cfg.OpenIMWebhookGRPCAddr)
	if err != nil {
		log.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	webhookgrpc.Serve(grpcServer, api)
	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Printf("webhook gRPC stopped: %v", err)
		}
	}()
	go api.RunStoryExpiry(context.Background())
	// Leave room for multipart headers while allowing the story/profile handlers
	// to enforce their own 10 MiB and 5 MiB media limits with a 413 response.
	server := &fasthttp.Server{Handler: api.Router(), Name: "pingmessenger-api", MaxRequestBodySize: 16 << 20}
	log.Printf("PingMessenger API listening on %s (%s)", cfg.HTTPAddr, cfg.AppEnv)
	if err := server.ListenAndServe(cfg.HTTPAddr); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
