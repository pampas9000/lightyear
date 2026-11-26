package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	FrontendURL string `koanf:"FRONTEND_URL"`
	DB          DatabaseConfig
	Redis       RedisConfig
	S3          S3Config
	Retention   RetentionConfig
	GoogleOAuth GoogleOAuthConfig
	GithubOAuth GithubOAuthConfig
	Dispatch    DispatchConfig
	Quota       QuotaConfig
	RateLimit   RateLimitConfig
}

// QuotaConfig is the global per-user quota; 0 is unlimited. Users can be
// given their own values (users.storage_quota_bytes, users.monthly_unit_quota).
type QuotaConfig struct {
	// StorageBytes bounds the bytes a user's stored sources and outputs take.
	StorageBytes int64 `koanf:"QUOTA_STORAGE_BYTES"`
	// MonthlyUnits bounds the processing units (encode rounds) a user's jobs
	// use in a calendar month (UTC).
	MonthlyUnits int64 `koanf:"QUOTA_MONTHLY_UNITS"`
}

// RateLimitConfig is the token bucket for /v1 write requests, per user.
// PerMinute 0 turns rate limiting off.
type RateLimitConfig struct {
	PerMinute int `koanf:"RATE_LIMIT_WRITE_PER_MINUTE"`
	Burst     int `koanf:"RATE_LIMIT_WRITE_BURST"`
}

// DispatchConfig holds orchestrator limits. Zero values take the
// orchestrator's defaults.
type DispatchConfig struct {
	MaxProcessingPerUser int           `koanf:"DISPATCH_MAX_PROCESSING_PER_USER"`
	ExecutionLimit       time.Duration `koanf:"EXECUTION_LIMIT"`
	TargetExecutionLimit time.Duration `koanf:"TARGET_EXECUTION_LIMIT"`
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

// Validate reports the S3 variables that are missing. The endpoint and
// region may be empty (AWS, and "auto" for R2); the bucket and credentials
// may not.
func (c S3Config) Validate() error {
	var missing []string
	for _, v := range []struct{ name, value string }{
		{"S3_BUCKET", c.Bucket}, {"S3_ACCESS_ID", c.AccessID}, {"S3_ACCESS_KEY", c.AccessKey},
	} {
		if strings.TrimSpace(v.value) == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing S3 configuration: set %s", strings.Join(missing, ", "))
	}
	return nil
}

// RetentionConfig is the global storage retention, in days; 0 keeps files
// forever. Users can be given their own values (users.*_retention_days).
type RetentionConfig struct {
	SourceDays int `koanf:"SOURCE_RETENTION_DAYS"`
	OutputDays int `koanf:"OUTPUT_RETENTION_DAYS"`
}

const (
	defaultDatabaseURL = "host=localhost port=5432 user=postgres password=postgres dbname=transcoder sslmode=disable"
	defaultRedisURL    = "redis://localhost:6379/0"
	defaultFrontendURL = "http://localhost:3000"
)

// Load reads runtime settings from environment variables and applies sane defaults.
func Load() Config {
	k := koanf.New(".")

	// 1. Set defaults
	k.Load(confmap.Provider(map[string]any{
		"APP_ENV":      "development",
		"DATABASE_URL": defaultDatabaseURL,
		"REDIS_URL":    defaultRedisURL,
		// Off by default: the schema changes only when someone runs
		// cmd/migrate (task migrate), never as a side effect of starting
		// an API instance.
		"AUTO_MIGRATE": false,
		"FRONTEND_URL": defaultFrontendURL,
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
		Region:    k.String("S3_REGION"),
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

	cfg.Dispatch = DispatchConfig{
		MaxProcessingPerUser: k.Int("DISPATCH_MAX_PROCESSING_PER_USER"),
		ExecutionLimit:       k.Duration("EXECUTION_LIMIT"),
		TargetExecutionLimit: k.Duration("TARGET_EXECUTION_LIMIT"),
	}
	cfg.Quota = QuotaConfig{
		StorageBytes: nonNegative(k, "QUOTA_STORAGE_BYTES", DefaultStorageQuotaBytes),
		MonthlyUnits: nonNegative(k, "QUOTA_MONTHLY_UNITS", DefaultMonthlyUnits),
	}
	cfg.RateLimit = RateLimitConfig{
		PerMinute: int(nonNegative(k, "RATE_LIMIT_WRITE_PER_MINUTE", DefaultRateLimitPerMinute)),
		Burst:     int(nonNegative(k, "RATE_LIMIT_WRITE_BURST", DefaultRateLimitBurst)),
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

// Quota and rate limit defaults, used when a variable is unset or empty.
const (
	DefaultStorageQuotaBytes  int64 = 10 << 30 // 10 GiB
	DefaultMonthlyUnits       int64 = 10_000
	DefaultRateLimitPerMinute int64 = 60
	DefaultRateLimitBurst     int64 = 30
)

// nonNegative reads a non-negative integer like retentionDays: unset, empty
// or invalid values fall back to def, so 0 (unlimited, or off) must be
// explicit.
func nonNegative(k *koanf.Koanf, key string, def int64) int64 {
	raw := strings.TrimSpace(k.String(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// IsProduction reports whether the service is running in production mode.
func (c Config) IsProduction() bool {
	return strings.EqualFold(c.AppEnv, "production")
}

// ValidateAPI checks that the API binary has valid configuration before it
// dials any dependency. In production, development defaults for DATABASE_URL,
// REDIS_URL and FRONTEND_URL are rejected, and FRONTEND_URL must be HTTPS on a
// non-loopback host.
func (c Config) ValidateAPI() error {
	if err := c.ValidateOrchestrator(); err != nil {
		return err
	}
	if !c.IsProduction() {
		return nil
	}
	return validateProductionFrontendURL(c.FrontendURL)
}

// ValidateOrchestrator checks that the orchestrator binary has valid
// configuration before it dials any dependency.
func (c Config) ValidateOrchestrator() error {
	if err := c.S3.Validate(); err != nil {
		return err
	}
	if !c.IsProduction() {
		return nil
	}
	if err := c.ValidateMigrate(); err != nil {
		return err
	}
	if redisURL := strings.TrimSpace(c.Redis.URL); redisURL == "" || redisURL == defaultRedisURL {
		return fmt.Errorf("REDIS_URL must be explicitly configured in production")
	}
	return nil
}

// ValidateMigrate checks that the migration binary has valid database
// configuration in production.
func (c Config) ValidateMigrate() error {
	if !c.IsProduction() {
		return nil
	}
	if dbURL := strings.TrimSpace(c.DB.URL); dbURL == "" || dbURL == defaultDatabaseURL {
		return fmt.Errorf("DATABASE_URL must be explicitly configured in production")
	}
	return nil
}

func validateProductionFrontendURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == defaultFrontendURL {
		return fmt.Errorf("FRONTEND_URL must be explicitly configured in production")
	}
	u, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return fmt.Errorf("FRONTEND_URL must be an https:// URL in production")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return fmt.Errorf("FRONTEND_URL must not point to localhost in production")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return fmt.Errorf("FRONTEND_URL must not point to a loopback address in production")
	}
	return nil
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
