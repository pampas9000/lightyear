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
