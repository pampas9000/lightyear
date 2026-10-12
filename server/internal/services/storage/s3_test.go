package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

func TestPresignPutObjectCarriesChecksum(t *testing.T) {
	client := s3sdk.New(s3sdk.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint: aws.String("https://example.r2.cloudflarestorage.com"),
		UsePathStyle: true,
	})
	svc := NewS3Service(client)

	const checksum = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=" // SHA-256 of ""
	raw, err := svc.PresignPutObject(context.Background(), "bucket", "uploads/u/k.png", "image/png", checksum, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	// The SDK hoists the checksum into the signed query string, so the browser
	// sends no extra header and cannot change the value.
	if got := q.Get("X-Amz-Checksum-Sha256"); got != checksum {
		t.Errorf("X-Amz-Checksum-Sha256 = %q, want %q", got, checksum)
	}
	// The browser cannot add an SDK-computed checksum; signing one would make
	// every upload fail.
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-checksum-crc") || lk == "x-amz-sdk-checksum-algorithm" {
			t.Errorf("unexpected query parameter %s", k)
		}
	}
	if signed := q.Get("X-Amz-SignedHeaders"); signed != "host" {
		t.Errorf("X-Amz-SignedHeaders = %q, want only host", signed)
	}
}

// TestPresignPutObjectChecksumEnforcedByStore proves against a real bucket
// that a body not matching the presigned checksum is rejected. It writes one
// small object, so it only runs when STORAGE_CHECKSUM_TEST=1 and the S3_*
// variables are set explicitly.
func TestPresignPutObjectChecksumEnforcedByStore(t *testing.T) {
	if os.Getenv("STORAGE_CHECKSUM_TEST") != "1" {
		t.Skip("set STORAGE_CHECKSUM_TEST=1 and S3_* to run against a real bucket")
	}
	bucket := os.Getenv("S3_BUCKET")
	client := s3sdk.New(s3sdk.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider(os.Getenv("S3_ACCESS_ID"), os.Getenv("S3_ACCESS_KEY"), ""),
		BaseEndpoint: aws.String(os.Getenv("S3_ENDPOINT")),
		UsePathStyle: true,
	})
	svc := NewS3Service(client)
	ctx := context.Background()
	key := "checksum-test/" + uuid.NewString()
	t.Cleanup(func() { _ = svc.DeleteObject(ctx, bucket, key) })

	body := []byte("lightyear checksum test")
	sum := sha256.Sum256(body)
	checksum := base64.StdEncoding.EncodeToString(sum[:])

	put := func(payload []byte) int {
		t.Helper()
		u, err := svc.PresignPutObject(ctx, bucket, key, "text/plain", checksum, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPut, u, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "text/plain")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := put([]byte("tampered body")); code < 400 {
		t.Fatalf("mismatched body was accepted with status %d", code)
	}
	if code := put(body); code != http.StatusOK {
		t.Fatalf("matching body was rejected with status %d", code)
	}
}
