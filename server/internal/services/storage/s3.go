package storage

import (
	"context"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Service struct {
	client *s3.Client
}

func NewS3Service(client *s3.Client) *S3Service {
	return &S3Service{
		client: client,
	}
}

// UploadFile uploads an object to S3.
func (s *S3Service) UploadFile(ctx context.Context, bucket, key string, body io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	return err
}

// GetPresignedURL generates a presigned URL for downloading or uploading.
func (s *S3Service) GetPresignedURL(ctx context.Context, bucket, key string, isUpload bool, expiry time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})

	if isUpload {
		req, err := presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return "", err
		}
		return req.URL, nil
	}

	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// CreateMultipartUpload initiates a multipart upload.
func (s *S3Service) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string) (string, error) {
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}
	return *out.UploadId, nil
}

// SignPart generates a presigned URL for a specific part.
func (s *S3Service) SignPart(ctx context.Context, bucket, key, uploadId string, partNumber int32, expiry time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})

	req, err := presignClient.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadId),
		PartNumber: aws.Int32(partNumber),
	})
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// CompleteMultipartUpload finishes the multipart upload.
func (s *S3Service) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadId string, parts []types.CompletedPart) error {
	_, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadId),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	return err
}

// AbortMultipartUpload cancels the multipart upload.
func (s *S3Service) AbortMultipartUpload(ctx context.Context, bucket, key, uploadId string) error {
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadId),
	})
	return err
}

// ListParts lists the parts that have been uploaded so far.
func (s *S3Service) ListParts(ctx context.Context, bucket, key, uploadId string) ([]types.Part, error) {
	var parts []types.Part
	var partNumberMarker *string
	for {
		out, err := s.client.ListParts(ctx, &s3.ListPartsInput{
			Bucket:           aws.String(bucket),
			Key:              aws.String(key),
			UploadId:         aws.String(uploadId),
			PartNumberMarker: partNumberMarker,
		})
		if err != nil {
			return nil, err
		}
		parts = append(parts, out.Parts...)
		if out.IsTruncated != nil && !*out.IsTruncated {
			break
		}
		partNumberMarker = out.NextPartNumberMarker
	}
	return parts, nil
}

// HeadObject retrieves metadata for an object in S3.
func (s *S3Service) HeadObject(ctx context.Context, bucket, key string) (*s3.HeadObjectOutput, error) {
	return s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
}

