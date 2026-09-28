package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv           string
	HTTPAddr         string
	DatabaseURL      string
	JWTSigningSecret string
	JWTIssuer        string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	S3PublicBaseURL  string
	S3Bucket         string
	S3Endpoint       string
	OpenIMAPIBaseURL string
	OpenIMAPIToken   string
	EmailProvider    string
	SMTPAddress      string
	SMTPFrom         string
	AWSRegion        string
	AWSSNSTopicARN   string
}

func Load() (Config, error) {
	_ = godotenv.Load() // Production configuration comes from the process environment.

	accessTTL, err := duration("ACCESS_TOKEN_TTL", "15m")
	if err != nil {
		return Config{}, err
	}
	refreshTTL, err := duration("REFRESH_TOKEN_TTL", "720h")
	if err != nil {
		return Config{}, err
	}
	c := Config{
		AppEnv: value("APP_ENV", "development"), HTTPAddr: value("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"), JWTSigningSecret: os.Getenv("JWT_SIGNING_SECRET"),
		JWTIssuer: value("JWT_ISSUER", "pingmessenger"), AccessTokenTTL: accessTTL, RefreshTokenTTL: refreshTTL,
		S3PublicBaseURL: os.Getenv("S3_PUBLIC_BASE_URL"), OpenIMAPIBaseURL: os.Getenv("OPENIM_API_BASE_URL"),
		S3Bucket: os.Getenv("S3_BUCKET"), S3Endpoint: os.Getenv("S3_ENDPOINT"),
		OpenIMAPIToken: os.Getenv("OPENIM_API_TOKEN"),
		EmailProvider:  value("EMAIL_PROVIDER", "smtp"), SMTPAddress: os.Getenv("SMTP_ADDRESS"), SMTPFrom: os.Getenv("SMTP_FROM"), AWSRegion: os.Getenv("AWS_REGION"), AWSSNSTopicARN: os.Getenv("AWS_SNS_TOPIC_ARN"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if len(c.JWTSigningSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SIGNING_SECRET must contain at least 32 bytes")
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
