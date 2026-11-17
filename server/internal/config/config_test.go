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
