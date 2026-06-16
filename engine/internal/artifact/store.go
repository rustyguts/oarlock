// Package artifact implements the managed artifact store: metadata rows in the
// artifacts table plus object bytes in an S3-compatible store (SeaweedFS in dev,
// Cloudflare R2 in prod). It satisfies steps.ArtifactStore. All object keys are
// prefixed ws/{workspace_id}/ (hard rule 7).
package artifact

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

type Config struct {
	Endpoint      string // host:port (no scheme)
	AccessKey     string
	SecretKey     string
	Bucket        string
	Region        string
	UseSSL        bool
	RetentionDays int // 0 = keep output artifacts forever; >0 sets expires_at
}

type S3Store struct {
	pool      *pgxpool.Pool
	mc        *minio.Client
	bucket    string
	retention int
	log       *slog.Logger
}

// New connects to the object store, ensures the bucket exists, and returns a
// store. A nil store (returned on empty config by the caller) disables the
// container.run step.
func New(ctx context.Context, pool *pgxpool.Pool, cfg Config, log *slog.Logger) (*S3Store, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("artifact store: %w", err)
	}
	// The object store may still be starting; retry the first connection.
	var lastErr error
	for i := 0; i < 15; i++ {
		exists, err := mc.BucketExists(ctx, cfg.Bucket)
		if err == nil {
			if !exists {
				if err = mc.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
					return nil, fmt.Errorf("artifact store: make bucket: %w", err)
				}
				log.Info("artifact bucket created", "bucket", cfg.Bucket)
			}
			return &S3Store{pool: pool, mc: mc, bucket: cfg.Bucket, retention: cfg.RetentionDays, log: log}, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("artifact store: cannot reach %s: %w", cfg.Endpoint, lastErr)
}

func (s *S3Store) Lookup(ctx context.Context, workspaceID, id uuid.UUID) (steps.ArtifactRef, error) {
	var ref steps.ArtifactRef
	err := s.pool.QueryRow(ctx, `
		select id, name, size, content_type, key from artifacts
		where workspace_id = $1 and id = $2`, workspaceID, id).
		Scan(&ref.ID, &ref.Name, &ref.Size, &ref.ContentType, &ref.Key)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ref, fmt.Errorf("artifact %s not found", id)
		}
		return ref, err
	}
	return ref, nil
}

func (s *S3Store) Record(ctx context.Context, rec steps.ArtifactRecord) (steps.ArtifactRef, error) {
	var ref steps.ArtifactRef
	ct := rec.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	src := rec.Source
	if src == "" {
		src = "output"
	}
	// Output artifacts get a retention TTL (so GC reclaims them); uploads are
	// user-managed and never auto-expire.
	var expiresAt *time.Time
	if src == "output" && s.retention > 0 {
		t := time.Now().Add(time.Duration(s.retention) * 24 * time.Hour)
		expiresAt = &t
	}
	err := s.pool.QueryRow(ctx, `
		insert into artifacts (workspace_id, run_id, task_id, key, name, size, content_type, source, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		returning id, name, size, content_type, key`,
		rec.WorkspaceID, rec.RunID, rec.TaskID, rec.Key, rec.Name, rec.Size, ct, src, expiresAt).
		Scan(&ref.ID, &ref.Name, &ref.Size, &ref.ContentType, &ref.Key)
	return ref, err
}

func (s *S3Store) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	// Stat first so a missing key fails here rather than on first read.
	if _, err := s.mc.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err != nil {
		return nil, fmt.Errorf("artifact download %s: %w", key, err)
	}
	obj, err := s.mc.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func (s *S3Store) Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if size <= 0 {
		size = -1 // unknown length → streamed multipart
	}
	_, err := s.mc.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("artifact upload %s: %w", key, err)
	}
	return nil
}

func (s *S3Store) Presign(ctx context.Context, method, key string, ttl time.Duration) (string, error) {
	var u *url.URL
	var err error
	switch strings.ToUpper(method) {
	case "GET":
		u, err = s.mc.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	case "PUT":
		u, err = s.mc.PresignedPutObject(ctx, s.bucket, key, ttl)
	default:
		return "", fmt.Errorf("presign: unsupported method %q", method)
	}
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	return s.mc.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *S3Store) OutputKey(workspaceID, runID, taskID uuid.UUID, name string) string {
	return fmt.Sprintf("ws/%s/runs/%s/tasks/%s/out/%s", workspaceID, runID, taskID, safeName(name))
}

func (s *S3Store) UploadKey(workspaceID, artifactID uuid.UUID, name string) string {
	return fmt.Sprintf("ws/%s/uploads/%s/%s", workspaceID, artifactID, safeName(name))
}

// safeName reduces a user-supplied name to a single path segment.
func safeName(name string) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == ".." {
		return "file"
	}
	return base
}
