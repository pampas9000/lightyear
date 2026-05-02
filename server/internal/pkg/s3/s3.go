package s3

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"transcoder/server/internal/config"
)

// NewClient builds a raw AWS S3 client.
func NewClient(ctx context.Context, cfg config.S3Config) (*s3.Client, error) {
	var baseEndpoint *string
	if cfg.Endpoint != "" {
		baseEndpoint = aws.String(cfg.Endpoint)
	}

	region := cfg.Region
	if region == "" {
		region = "auto"
	}

	client := s3.New(s3.Options{
		Region:       region,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessID, cfg.AccessKey, "")),
		BaseEndpoint: baseEndpoint,
		UsePathStyle: true,
	})

	return client, nil
}
