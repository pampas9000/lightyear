package config

import (
	"strconv"
	"strings"

	"github.com/knadh/koanf/parsers/dotenv"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Config holds runtime settings for the API service.
type Config struct {
	AppEnv      string `koanf:"APP_ENV"`
	ListenAddr  string `koanf:"SERVER_ADDR"`
	JWTSecret   string `koanf:"JWT_SECRET"`
	FrontendURL string `koanf:"FRONTEND_URL"`
	DB          DatabaseConfig
	Redis       RedisConfig
	S3          S3Config
	Retention   RetentionConfig
	GoogleOAuth GoogleOAuthConfig
	GithubOAuth GithubOAuthConfig
}

type GoogleOAuthConfig struct {
	ClientID     string `koanf:"GOOGLE_CLIENT_ID"`
	ClientSecret string `koanf:"GOOGLE_CLIENT_SECRET"`
	CallbackURL  string `koanf:"GOOGLE_CALLBACK_URL"`
}

type GithubOAuthConfig struct {
	ClientID     string `koanf:"GITHUB_CLIENT_ID"`
	ClientSecret string `koanf:"GITHUB_CLIENT_SECRET"`
	CallbackURL  string `koanf:"GITHUB_CALLBACK_URL"`
}

type DatabaseConfig struct {
	URL         string `koanf:"DATABASE_URL"`
	AutoMigrate bool   `koanf:"AUTO_MIGRATE"`
}

type RedisConfig struct {
	URL string `koanf:"REDIS_URL"`
}

type S3Config struct {
	Endpoint  string `koanf:"S3_ENDPOINT"`
	AccessID  string `koanf:"S3_ACCESS_ID"`
	AccessKey string `koanf:"S3_ACCESS_KEY"`
	Region    string `koanf:"S3_REGION"`
	Bucket    string `koanf:"S3_BUCKET"`
}

// RetentionConfig is the global storage retention, in days; 0 keeps files
// forever. Users can be given their own values (users.*_retention_days).
type RetentionConfig struct {
	SourceDays int `koanf:"SOURCE_RETENTION_DAYS"`
	OutputDays int `koanf:"OUTPUT_RETENTION_DAYS"`
}

// Load reads runtime settings from environment variables and applies sane defaults.
func Load() Config {
	k := koanf.New(".")

	// 1. Set defaults
	k.Load(confmap.Provider(map[string]any{
		"APP_ENV":      "development",
		"DATABASE_URL": "host=localhost port=5432 user=postgres password=postgres dbname=transcoder sslmode=disable",
		"REDIS_URL":    "redis://localhost:6379/0",
		"JWT_SECRET":   "dev-secret-change-me-in-production",
		"AUTO_MIGRATE": true,
		"FRONTEND_URL": "http://localhost:3000",
	}, "."), nil)

	// 2. Read from .env file if available
	if err := k.Load(file.Provider(".env"), dotenv.Parser()); err != nil {
		_ = k.Load(file.Provider("../.env"), dotenv.Parser())
	}

	// 3. Bind environment variables
	k.Load(env.Provider("", ".", func(s string) string {
		return strings.ToUpper(s)
	}), nil)

	// 4. Handle specialized logic for SERVER_ADDR / PORT
	addr := k.String("SERVER_ADDR")
	if addr == "" {
		port := k.String("PORT")
		if port != "" {
			addr = normalizeListenAddr(port)
		} else {
			// Listening on both IPv6 and IPv4
			addr = "[::]:8080"
		}
	}

	// 5. Construct the nested structure from flat keys
	var cfg Config
	cfg.AppEnv = k.String("APP_ENV")
	cfg.ListenAddr = addr
	cfg.JWTSecret = k.String("JWT_SECRET")
	cfg.FrontendURL = k.String("FRONTEND_URL")

	cfg.DB = DatabaseConfig{
		URL:         k.String("DATABASE_URL"),
		AutoMigrate: k.Bool("AUTO_MIGRATE"),
	}
	cfg.Redis = RedisConfig{
		URL: k.String("REDIS_URL"),
	}
	cfg.S3 = S3Config{
		Endpoint:  k.String("S3_ENDPOINT"),
		AccessID:  k.String("S3_ACCESS_ID"),
		AccessKey: k.String("S3_ACCESS_KEY"),
		Bucket:    k.String("S3_BUCKET"),
	}
	cfg.Retention = RetentionConfig{
		SourceDays: retentionDays(k, "SOURCE_RETENTION_DAYS"),
		OutputDays: retentionDays(k, "OUTPUT_RETENTION_DAYS"),
	}
	cfg.GoogleOAuth = GoogleOAuthConfig{
		ClientID:     k.String("GOOGLE_CLIENT_ID"),
		ClientSecret: k.String("GOOGLE_CLIENT_SECRET"),
		CallbackURL:  k.String("GOOGLE_CALLBACK_URL"),
	}
	cfg.GithubOAuth = GithubOAuthConfig{
		ClientID:     k.String("GITHUB_CLIENT_ID"),
		ClientSecret: k.String("GITHUB_CLIENT_SECRET"),
		CallbackURL:  k.String("GITHUB_CALLBACK_URL"),
	}

	return cfg
}

// DefaultRetentionDays applies when a retention variable is unset or empty.
const DefaultRetentionDays = 30

// retentionDays reads a retention in days. Unset, empty or invalid values
// fall back to DefaultRetentionDays, so an empty line copied from
// .env.example does not silently mean "keep forever"; 0 must be explicit.
func retentionDays(k *koanf.Koanf, key string) int {
	raw := strings.TrimSpace(k.String(key))
	if raw == "" {
		return DefaultRetentionDays
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return DefaultRetentionDays
	}
	return n
}

// IsProduction reports whether the service is running in production mode.
func (c Config) IsProduction() bool {
	return strings.EqualFold(c.AppEnv, "production")
}

func normalizeListenAddr(port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		return ""
	}
	if strings.Contains(port, ":") {
		return port
	}
	return ":" + port
}
