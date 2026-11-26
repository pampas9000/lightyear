package config

import "testing"

// Every S3 variable documented in .env.example must reach S3Config.
// S3_REGION was once dropped, so presigned URLs were always scoped to
// "auto": R2 accepts that, but MinIO and garage reject the signature.
func TestLoadReadsS3Settings(t *testing.T) {
	t.Chdir(t.TempDir()) // no .env next to the test
	t.Setenv("S3_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("S3_ACCESS_ID", "id")
	t.Setenv("S3_ACCESS_KEY", "secret")
	t.Setenv("S3_BUCKET", "bucket")
	t.Setenv("S3_REGION", "us-east-1")

	got := Load().S3
	want := S3Config{Endpoint: "http://127.0.0.1:9000", AccessID: "id", AccessKey: "secret", Region: "us-east-1", Bucket: "bucket"}
	if got != want {
		t.Fatalf("S3 config = %+v, want %+v", got, want)
	}
}

// Unset or invalid quota variables take the defaults; 0 must be explicit.
func TestLoadQuotaAndRateLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("QUOTA_STORAGE_BYTES", "")
	t.Setenv("QUOTA_MONTHLY_UNITS", "0")
	t.Setenv("RATE_LIMIT_WRITE_PER_MINUTE", "-5")
	t.Setenv("RATE_LIMIT_WRITE_BURST", "7")
	cfg := Load()
	if cfg.Quota != (QuotaConfig{StorageBytes: DefaultStorageQuotaBytes, MonthlyUnits: 0}) {
		t.Fatalf("quota = %+v", cfg.Quota)
	}
	if cfg.RateLimit != (RateLimitConfig{PerMinute: int(DefaultRateLimitPerMinute), Burst: 7}) {
		t.Fatalf("rate limit = %+v", cfg.RateLimit)
	}
}

// The orchestrator and the API refuse to start without a bucket and
// credentials; the error names what is missing.
func TestS3ConfigValidate(t *testing.T) {
	if err := (S3Config{Bucket: "b", AccessID: "id", AccessKey: "k"}).Validate(); err != nil {
		t.Fatalf("complete config: %v", err)
	}
	err := (S3Config{AccessID: "id", AccessKey: " "}).Validate()
	if err == nil {
		t.Fatal("missing bucket and key accepted")
	}
	if got, want := err.Error(), "missing S3 configuration: set S3_BUCKET, S3_ACCESS_KEY"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestValidateProduction(t *testing.T) {
	validProd := func() Config {
		return Config{
			AppEnv:      "production",
			FrontendURL: "https://app.example.com",
			DB:          DatabaseConfig{URL: "postgres://user:pass@db.example.com:5432/lightyear?sslmode=require"},
			Redis:       RedisConfig{URL: "rediss://redis.example.com:6379/0"},
			S3:          S3Config{Bucket: "b", AccessID: "id", AccessKey: "secret"},
		}
	}

	if err := validProd().ValidateAPI(); err != nil {
		t.Fatalf("valid production API config rejected: %v", err)
	}
	if err := validProd().ValidateOrchestrator(); err != nil {
		t.Fatalf("valid production orchestrator config rejected: %v", err)
	}
	if err := (Config{AppEnv: "production", DB: DatabaseConfig{URL: validProd().DB.URL}}).ValidateMigrate(); err != nil {
		t.Fatalf("valid production migrate config rejected: %v", err)
	}

	// Development accepts local defaults when S3 is configured.
	dev := Config{
		AppEnv:      "development",
		FrontendURL: defaultFrontendURL,
		DB:          DatabaseConfig{URL: defaultDatabaseURL},
		Redis:       RedisConfig{URL: defaultRedisURL},
		S3:          S3Config{Bucket: "b", AccessID: "id", AccessKey: "secret"},
	}
	if err := dev.ValidateAPI(); err != nil {
		t.Fatalf("development config rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
		check  func(Config) error
	}{
		{"default DATABASE_URL", func(c *Config) { c.DB.URL = defaultDatabaseURL }, Config.ValidateMigrate},
		{"empty DATABASE_URL", func(c *Config) { c.DB.URL = " " }, Config.ValidateMigrate},
		{"default REDIS_URL", func(c *Config) { c.Redis.URL = defaultRedisURL }, Config.ValidateOrchestrator},
		{"default FRONTEND_URL", func(c *Config) { c.FrontendURL = defaultFrontendURL }, Config.ValidateAPI},
		{"http FRONTEND_URL", func(c *Config) { c.FrontendURL = "http://app.example.com" }, Config.ValidateAPI},
		{"localhost FRONTEND_URL", func(c *Config) { c.FrontendURL = "https://localhost:3000" }, Config.ValidateAPI},
		{"loopback IP FRONTEND_URL", func(c *Config) { c.FrontendURL = "https://127.0.0.1:3000" }, Config.ValidateAPI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validProd()
			tc.mutate(&cfg)
			if err := tc.check(cfg); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
