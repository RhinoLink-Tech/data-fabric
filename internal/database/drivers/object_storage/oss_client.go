package object_storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// OSSClient wraps Alibaba Cloud OSS client
type OSSClient struct {
	client *oss.Client
	bucket *oss.Bucket
	config *OSSConfig
}

// OSSConfig holds Alibaba Cloud OSS configuration
type OSSConfig struct {
	Endpoint        string // OSS endpoint (e.g., https://oss-cn-hangzhou.aliyuncs.com)
	AccessKeyID     string
	AccessKeySecret string
	BucketName      string
	SecurityToken   string // For STS token
	ForcePathStyle  bool   // Use path-style addressing for compatible test endpoints.
}

// NewOSSClient creates a new Alibaba Cloud OSS client
func NewOSSClient(ctx context.Context, config *OSSConfig) (*OSSClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("OSS configuration is required")
	}
	if config.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if config.AccessKeyID == "" {
		return nil, fmt.Errorf("access key ID is required")
	}
	if config.AccessKeySecret == "" {
		return nil, fmt.Errorf("access key secret is required")
	}
	if config.BucketName == "" {
		return nil, fmt.Errorf("bucket name is required")
	}

	// Create OSS client
	clientOptions := []oss.ClientOption{oss.ForcePathStyle(config.ForcePathStyle)}
	var client *oss.Client
	var err error

	if config.SecurityToken != "" {
		// Use STS token
		clientOptions = append(clientOptions, oss.SecurityToken(config.SecurityToken))
	}
	client, err = oss.New(config.Endpoint, config.AccessKeyID, config.AccessKeySecret, clientOptions...)

	if err != nil {
		return nil, fmt.Errorf("failed to create OSS client: %w", err)
	}

	// Get bucket
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket: %w", err)
	}

	return &OSSClient{
		client: client,
		bucket: bucket,
		config: config,
	}, nil
}

// ListObjects lists objects in a bucket with optional prefix
func (c *OSSClient) ListObjects(ctx context.Context, prefix string, maxKeys int) ([]OSSObject, error) {
	if c == nil || c.bucket == nil {
		return nil, fmt.Errorf("OSS client is not initialized")
	}
	if err := ossContextError(ctx); err != nil {
		return nil, err
	}
	options := []oss.Option{
		oss.Prefix(prefix),
		oss.MaxKeys(maxKeys),
		oss.WithContext(ctx),
	}

	lor, err := c.bucket.ListObjects(options...)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	objects := make([]OSSObject, 0, len(lor.Objects))
	for _, obj := range lor.Objects {
		objects = append(objects, OSSObject{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			ETag:         obj.ETag,
			Type:         obj.Type,
		})
	}

	return objects, nil
}

// GetObject retrieves an object from OSS
func (c *OSSClient) GetObject(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.bucket == nil {
		return nil, fmt.Errorf("OSS client is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("OSS object key is required")
	}
	if err := ossContextError(ctx); err != nil {
		return nil, err
	}
	reader, err := c.bucket.GetObject(key, oss.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}
	if err := ossContextError(ctx); err != nil {
		return nil, err
	}

	return data, nil
}

// GetObjectMetadata retrieves object metadata without downloading the content
func (c *OSSClient) GetObjectMetadata(ctx context.Context, key string) (*OSSObjectMetadata, error) {
	if c == nil || c.bucket == nil {
		return nil, fmt.Errorf("OSS client is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("OSS object key is required")
	}
	if err := ossContextError(ctx); err != nil {
		return nil, err
	}
	meta, err := c.bucket.GetObjectMeta(key, oss.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	// Initialize basic metadata
	metadata := &OSSObjectMetadata{
		Key: key,
	}

	// Parse Content-Length from the metadata
	if contentLengthStr := meta.Get("Content-Length"); contentLengthStr != "" {
		if contentLength, parseErr := strconv.ParseInt(contentLengthStr, 10, 64); parseErr == nil {
			metadata.ContentLength = contentLength
		}
	}

	// Parse Last-Modified timestamp
	if lastModifiedStr := meta.Get("Last-Modified"); lastModifiedStr != "" {
		if lastModified, parseErr := time.Parse(time.RFC1123, lastModifiedStr); parseErr == nil {
			metadata.LastModified = lastModified
		}
	}

	// Set ETag
	if etag := meta.Get("ETag"); etag != "" {
		metadata.ETag = etag
	}

	if contentType := meta.Get("Content-Type"); contentType != "" {
		metadata.ContentType = contentType
	}

	if contentEncoding := meta.Get("Content-Encoding"); contentEncoding != "" {
		metadata.ContentEncoding = contentEncoding
	}

	if cacheControl := meta.Get("Cache-Control"); cacheControl != "" {
		metadata.CacheControl = cacheControl
	}

	return metadata, nil
}

func ossContextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return ctx.Err()
}

// PutObject uploads an object to OSS
func (c *OSSClient) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	options := []oss.Option{}

	if contentType != "" {
		options = append(options, oss.ContentType(contentType))
	}

	err := c.bucket.PutObject(key, bytes.NewReader(data), options...)
	if err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	return nil
}

// DeleteObject deletes an object from OSS
func (c *OSSClient) DeleteObject(ctx context.Context, key string) error {
	err := c.bucket.DeleteObject(key)
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

// CopyObject copies an object within OSS
func (c *OSSClient) CopyObject(ctx context.Context, srcKey, destKey string) error {
	_, err := c.bucket.CopyObject(srcKey, destKey)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// PresignURL generates a presigned URL for temporary access
func (c *OSSClient) PresignURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	url, err := c.bucket.SignURL(key, oss.HTTPGet, int64(expiration.Seconds()))
	if err != nil {
		return "", fmt.Errorf("failed to presign URL: %w", err)
	}

	return url, nil
}

// ObjectExists checks if an object exists
func (c *OSSClient) ObjectExists(ctx context.Context, key string) (bool, error) {
	exists, err := c.bucket.IsObjectExist(key)
	if err != nil {
		return false, fmt.Errorf("failed to check object existence: %w", err)
	}
	return exists, nil
}

// GetObjectSize gets the size of an object
func (c *OSSClient) GetObjectSize(ctx context.Context, key string) (int64, error) {
	metadata, err := c.GetObjectMetadata(ctx, key)
	if err != nil {
		return 0, err
	}
	return metadata.ContentLength, nil
}

// OSSObject represents an OSS object
type OSSObject struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	Type         string
}

// OSSObjectMetadata represents OSS object metadata
type OSSObjectMetadata struct {
	Key             string
	ContentLength   int64
	ContentType     string
	ContentEncoding string
	LastModified    time.Time
	ETag            string
	CacheControl    string
}

// ListFilesByExtension lists files with a specific extension
func (c *OSSClient) ListFilesByExtension(ctx context.Context, prefix, extension string) ([]OSSObject, error) {
	objects, err := c.ListObjects(ctx, prefix, 1000)
	if err != nil {
		return nil, err
	}

	var filtered []OSSObject
	for _, obj := range objects {
		if hasExtension(obj.Key, extension) {
			filtered = append(filtered, obj)
		}
	}

	return filtered, nil
}

// GetBucketInfo returns bucket information
func (c *OSSClient) GetBucketInfo(ctx context.Context) *OSSBucketInfo {
	return &OSSBucketInfo{
		Name:     c.config.BucketName,
		Endpoint: c.config.Endpoint,
		Region:   extractRegionFromEndpoint(c.config.Endpoint),
	}
}

// OSSBucketInfo represents OSS bucket information
type OSSBucketInfo struct {
	Name     string
	Endpoint string
	Region   string
}

// extractRegionFromEndpoint extracts region from OSS endpoint
func extractRegionFromEndpoint(endpoint string) string {
	// OSS endpoint format: oss-cn-hangzhou.aliyuncs.com
	// Extract region: cn-hangzhou
	parts := fmt.Sprintf("%s", endpoint)
	if len(parts) > 3 {
		return parts[4:15] // Simplified extraction
	}
	return "unknown"
}
