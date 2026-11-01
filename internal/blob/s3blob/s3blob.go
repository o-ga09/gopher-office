// Package s3blob は S3 互換のオブジェクトストレージ（MinIO・R2 など）に置く blob.Store。
// aws-sdk-go-v2 に依存するので、import してよいのは cmd/api と cmd/iconmigrate だけ（WASM・GameServer に入れない）。
package s3blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/o-ga09/gopher-office/internal/blob"
)

// Config は接続先。Endpoint が空なら AWS の既定のエンドポイント。
type Config struct {
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
}

type Store struct {
	c      *s3.Client
	bucket string
}

var _ blob.Store = (*Store)(nil)

func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3blob: bucket and credentials are required")
	}
	region := cfg.Region
	if region == "" {
		region = "auto"
	}
	opts := s3.Options{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		UsePathStyle: true,
		// S3 互換実装は追加のチェックサム（CRC32 など）に対応しないことがある。必要なときだけ付ける
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	return &Store{c: s3.New(opts), bucket: cfg.Bucket}, nil
}

func (s *Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := s.c.PutObject(ctx, in); err != nil {
		return fmt.Errorf("s3blob: put %s: %w", key, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, blob.ErrNotFound
		}
		return nil, fmt.Errorf("s3blob: get %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	return io.ReadAll(out.Body)
}

// ConfigFromEnv は環境変数（S3_ENDPOINT / S3_BUCKET / S3_REGION / AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY）から Config を作る。
// 秘密の値をフラグに載せない（プロセス一覧やシェルの履歴に残るため）。
func ConfigFromEnv(getenv func(string) string) Config {
	return Config{
		Endpoint:  getenv("S3_ENDPOINT"),
		Bucket:    getenv("S3_BUCKET"),
		Region:    getenv("S3_REGION"),
		AccessKey: getenv("AWS_ACCESS_KEY_ID"),
		SecretKey: getenv("AWS_SECRET_ACCESS_KEY"),
	}
}
