package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv                       string
	HTTPAddr                     string
	DatabaseURL                  string
	JWTSigningSecret             string
	JWTIssuer                    string
	AccessTokenTTL               time.Duration
	RefreshTokenTTL              time.Duration
	S3PublicBaseURL              string
	S3Bucket                     string
	DocumentS3Bucket             string
	S3Endpoint                   string
	DocumentMaxBytes             int64
	DocumentMaxCount             int
	DocumentMaxBytes24h          int64
	DocumentRetention            time.Duration
	DocumentAllowedMIMEs         string
	OpenIMAPIBaseURL             string
	OpenIMConversationGRPCTarget string
	OpenIMAPIToken               string
	OpenIMSharedSecret           string
	OpenIMAdminUserID            string
	OpenIMWebhookGRPCAddr        string
	DeviceTokenEncryptionKey     string
	EmailProvider                string
	SMTPAddress                  string
	SMTPFrom                     string
	EmailMirrorSMTPAddress       string
	EmailMirrorFrom              string
	AWSRegion                    string
	AWSSNSTopicARN               string
	AWSSESFromEmail              string
	SMSMirrorTopicARN            string
	PingPayEventSecret           string
	SolanaRPCURL                 string
	SolanaRPCTimeout             time.Duration
}

func Load() (Config, error) {
	_ = godotenv.Load() // Production configuration comes from the process environment.

	accessTTL, err := duration("ACCESS_TOKEN_TTL", "15m")
	if err != nil {
		return Config{}, err
	}
	documentRetention, err := duration("DOCUMENT_RETENTION", "168h")
	if err != nil || documentRetention <= 0 || documentRetention > 7*24*time.Hour {
		return Config{}, fmt.Errorf("DOCUMENT_RETENTION must be between 1h and 168h")
	}
	documentMaxBytes, err := int64Value("DOCUMENT_MAX_BYTES", 100<<20)
	if err != nil || documentMaxBytes <= 0 {
		return Config{}, fmt.Errorf("DOCUMENT_MAX_BYTES must be a positive integer")
	}
	documentMaxCount, err := intValue("DOCUMENT_MAX_COUNT", 20)
	if err != nil || documentMaxCount <= 0 {
		return Config{}, fmt.Errorf("DOCUMENT_MAX_COUNT must be a positive integer")
	}
	documentMaxBytes24h, err := int64Value("DOCUMENT_MAX_BYTES_24H", 1<<30)
	if err != nil || documentMaxBytes24h <= 0 {
		return Config{}, fmt.Errorf("DOCUMENT_MAX_BYTES_24H must be a positive integer")
	}
	refreshTTL, err := duration("REFRESH_TOKEN_TTL", "720h")
	if err != nil {
		return Config{}, err
	}
	solanaTimeout, err := duration("SOLANA_RPC_TIMEOUT", "10s")
	if err != nil {
		return Config{}, err
	}
	c := Config{
		AppEnv: value("APP_ENV", "development"), HTTPAddr: value("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"), JWTSigningSecret: os.Getenv("JWT_SIGNING_SECRET"),
		JWTIssuer: value("JWT_ISSUER", "pingmessenger"), AccessTokenTTL: accessTTL, RefreshTokenTTL: refreshTTL,
		S3PublicBaseURL: os.Getenv("S3_PUBLIC_BASE_URL"), OpenIMAPIBaseURL: os.Getenv("OPENIM_API_BASE_URL"), OpenIMConversationGRPCTarget: os.Getenv("OPENIM_CONVERSATION_GRPC_TARGET"),
		S3Bucket: os.Getenv("S3_BUCKET"), DocumentS3Bucket: value("DOCUMENT_S3_BUCKET", os.Getenv("S3_BUCKET")), S3Endpoint: os.Getenv("S3_ENDPOINT"),
		DocumentMaxBytes: documentMaxBytes, DocumentMaxCount: documentMaxCount, DocumentMaxBytes24h: documentMaxBytes24h, DocumentRetention: documentRetention,
		DocumentAllowedMIMEs: value("DOCUMENT_ALLOWED_MIMES", "application/pdf,application/rtf,application/msword,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,application/vnd.ms-powerpoint,application/vnd.openxmlformats-officedocument.presentationml.presentation,text/plain,text/csv,application/zip"),
		OpenIMAPIToken:       os.Getenv("OPENIM_API_TOKEN"), OpenIMSharedSecret: os.Getenv("OPENIM_SHARED_SECRET"), OpenIMAdminUserID: value("OPENIM_ADMIN_USER_ID", "imAdmin"), OpenIMWebhookGRPCAddr: value("OPENIM_WEBHOOK_GRPC_ADDR", ":9090"),
		DeviceTokenEncryptionKey: os.Getenv("DEVICE_TOKEN_ENCRYPTION_KEY"),
		EmailProvider:            value("EMAIL_PROVIDER", "smtp"), SMTPAddress: os.Getenv("SMTP_ADDRESS"), SMTPFrom: os.Getenv("SMTP_FROM"), EmailMirrorSMTPAddress: os.Getenv("EMAIL_MIRROR_SMTP_ADDRESS"), EmailMirrorFrom: value("EMAIL_MIRROR_FROM", "e2e-mirror@pingmessenger.local"), AWSRegion: os.Getenv("AWS_REGION"), AWSSNSTopicARN: os.Getenv("AWS_SNS_TOPIC_ARN"), AWSSESFromEmail: os.Getenv("AWS_SES_FROM_EMAIL"), SMSMirrorTopicARN: os.Getenv("SMS_MIRROR_TOPIC_ARN"), PingPayEventSecret: os.Getenv("PINGPAY_EVENT_SECRET"),
		// No public RPC fallback in a deployed process; local development can opt
		// in through .env.example's devnet value.
		SolanaRPCURL: value("SOLANA_RPC_URL", ""), SolanaRPCTimeout: solanaTimeout,
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if len(c.JWTSigningSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SIGNING_SECRET must contain at least 32 bytes")
	}
	if c.DeviceTokenEncryptionKey == "" {
		return Config{}, fmt.Errorf("DEVICE_TOKEN_ENCRYPTION_KEY is required")
	}
	return c, nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func duration(key, fallback string) (time.Duration, error) {
	v, err := time.ParseDuration(value(key, fallback))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func int64Value(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	var n int64
	_, err := fmt.Sscan(v, &n)
	return n, err
}

func intValue(key string, fallback int) (int, error) {
	n, err := int64Value(key, int64(fallback))
	return int(n), err
}
