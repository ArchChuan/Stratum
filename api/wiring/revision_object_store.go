package wiring

import (
	"context"
	"fmt"
	"io"

	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgobjectstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type revisionMinIOClient struct{ client *minio.Client }

func (c revisionMinIOClient) PutObject(
	ctx context.Context, bucket, key string, reader io.Reader, size int64, options minio.PutObjectOptions,
) (minio.UploadInfo, error) {
	return c.client.PutObject(ctx, bucket, key, reader, size, options)
}

func (c revisionMinIOClient) GetObject(
	ctx context.Context, bucket, key string, options minio.GetObjectOptions,
) (io.ReadCloser, error) {
	return c.client.GetObject(ctx, bucket, key, options)
}

func (c revisionMinIOClient) RemoveObject(
	ctx context.Context, bucket, key string, options minio.RemoveObjectOptions,
) error {
	return c.client.RemoveObject(ctx, bucket, key, options)
}

func (c *Container) buildRevisionObjectStore(ctx context.Context) error {
	if c.RevisionObjectStore != nil {
		return nil
	}
	if c.Config.TracePayload.Endpoint == "" || c.Config.TracePayload.AccessKey == "" ||
		c.Config.TracePayload.SecretKey == "" || c.Config.TracePayload.Bucket == "" {
		return nil
	}
	client, err := minio.New(c.Config.TracePayload.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(
			c.Config.TracePayload.AccessKey, c.Config.TracePayload.SecretKey, "",
		),
		Secure: c.Config.TracePayload.UseTLS,
	})
	if err != nil {
		return fmt.Errorf("revision object client: %w", err)
	}
	initCtx, cancel := context.WithTimeout(ctx, constants.RevisionObjectStoreInitTimeout)
	defer cancel()
	exists, err := client.BucketExists(initCtx, c.Config.TracePayload.Bucket)
	if err != nil {
		return fmt.Errorf("revision object bucket check: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(initCtx, c.Config.TracePayload.Bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("revision object bucket create: %w", err)
		}
	}
	c.revisionObjectClient = client
	c.RevisionObjectStore = pkgobjectstore.NewEncryptedStore(
		revisionMinIOClient{client: client}, c.Config.TracePayload.Bucket, c.Platform.AESKey,
	)
	return nil
}
