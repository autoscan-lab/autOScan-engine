package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type r2Client struct {
	client *minio.Client
	bucket string
}

func newR2Client(_ context.Context, c config) (*r2Client, error) {
	if err := c.requireR2(); err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s.r2.cloudflarestorage.com", c.r2AccountID)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.r2AccessKey, c.r2SecretKey, ""),
		Secure: true,
		Region: "auto",
	})
	if err != nil {
		return nil, fmt.Errorf("creating r2 client: %w", err)
	}
	return &r2Client{client: client, bucket: c.r2BucketName}, nil
}

// downloadObject fetches a single object. Returns false if the key doesn't exist.
func (r *r2Client) downloadObject(ctx context.Context, key, localPath string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return false, err
	}
	err := r.client.FGetObject(ctx, r.bucket, key, localPath, minio.GetObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, fmt.Errorf("getting %s: %w", key, err)
	}
	return true, nil
}

// downloadPrefix fetches every object under prefix into destDir, preserving
// the relative key structure. Returns the list of downloaded keys.
func (r *r2Client) downloadPrefix(ctx context.Context, prefix, destDir string) ([]string, error) {
	var keys []string
	for obj := range r.client.ListObjects(ctx, r.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, obj.Err)
		}
		if obj.Key == "" || strings.HasSuffix(obj.Key, "/") {
			continue
		}
		rel := strings.TrimPrefix(obj.Key, prefix)
		localPath := filepath.Join(destDir, filepath.FromSlash(rel))
		if _, err := r.downloadObject(ctx, obj.Key, localPath); err != nil {
			return nil, err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}
