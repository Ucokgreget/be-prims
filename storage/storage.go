package storage

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var client *minio.Client
var bucket string

func Init() error {
	endpoint := envOr("MINIO_ENDPOINT", "localhost:9000")
	useSSL := strings.EqualFold(os.Getenv("MINIO_USE_SSL"), "true")
	bucket = envOr("MINIO_BUCKET", "prims")

	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("MINIO_ACCESS_KEY"), os.Getenv("MINIO_SECRET_KEY"), ""),
		Secure: useSSL,
	})

	if err != nil {
		return fmt.Errorf("failed to create minio client: %w", err)
	}
	client = cl

	exist, err := client.BucketExists(context.Background(), bucket)
	if err != nil {
		return fmt.Errorf("Gagal menyambung ke bucket minio: %w", err)
	}

	if !exist {
		if err := client.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("Gagal membuat bucket minio: %w", err)
		}
		fmt.Fprintf(os.Stderr, "storage: bucket %q disimpan di %s\n", bucket, endpoint)
	}
	return nil
}

func Upload(ctx context.Context, key string, fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage: failed to open file: %v\n", err)
		return err
	}

	if _, err := client.PutObject(ctx, bucket, key, f, fh.Size, minio.PutObjectOptions{
		ContentType: contentTypeFor(key, fh),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "storage: failed to upload object: %v\n", err)
		return err
	}
	return nil
}

func Open(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func Stat(ctx context.Context, key string) (minio.ObjectInfo, error) {
	return client.StatObject(ctx, bucket, key, minio.GetObjectOptions{})
}

func Delete(ctx context.Context, key []string) {
	for _, key := range key {
		if err := client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
			fmt.Fprintf(os.Stderr, "storage: failed to delete object: %q: %v\n", key, err)
		}
	}
}

func ObjectKeyFromFileUrl(fileUrl string) string {
	key := strings.TrimPrefix(fileUrl, "/uploads")
	key = strings.TrimPrefix(key, "uploads/")
	return filepath.ToSlash(key)
}

func contentTypeFor(key string, fh *multipart.FileHeader) string {
	if ct := mime.TypeByExtension(filepath.Ext(key)); ct != "" {
		return ct
	}
	return fh.Header.Get("Content-Type")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
