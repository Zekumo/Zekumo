package storage

import (
	"context"
	"errors"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 talks to any S3-compatible object store (AWS S3, MinIO, R2, OSS).
type S3 struct {
	client *minio.Client
	bucket string
}

func NewS3(endpoint, region, bucket, accessKey, secretKey string, useSSL bool) (*S3, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	return &S3{client: client, bucket: bucket}, nil
}

func (s *S3) PresignUpload(ctx context.Context, key string) (string, error) {
	u, err := s.client.PresignedPutObject(ctx, s.bucket, key, PresignTTL)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3) PresignDownload(ctx context.Context, key, filename string) (string, error) {
	return s.PresignDownloadTTL(ctx, key, filename, PresignTTL)
}

func (s *S3) PresignDownloadTTL(ctx context.Context, key, filename string, ttl time.Duration) (string, error) {
	params := url.Values{}
	params.Set("response-content-disposition", `attachment; filename="`+filename+`"`)
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, boundedPresignTTL(ttl), params)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3) Stat(ctx context.Context, key string) (int64, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return info.Size, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *S3) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{})
	return err
}

// EnsureBucket creates the bucket if it does not exist yet (handy for MinIO
// in docker-compose).
func (s *S3) EnsureBucket(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil || exists {
		return err
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}
