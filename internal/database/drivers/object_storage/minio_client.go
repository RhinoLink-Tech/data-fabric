package object_storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinIOClient wraps MinIO Go client
type MinIOClient struct {
	client    *minio.Client
	endpoint  string
	accessKey string
	secretKey string
	region    string
	bucket    string
	secure    bool // Use HTTPS
}

// MinIOConfig holds MinIO configuration
type MinIOConfig struct {
	Endpoint  string // MinIO server endpoint (e.g., localhost:9000)
	AccessKey string // Access key (username)
	SecretKey string // Secret key (password)
	Bucket    string // Default bucket
	Region    string // Region (default: us-east-1)
	Secure    bool   // Use HTTPS (default: false for local)
	Token     string // Session token for temporary credentials
}

// NewMinIOClient creates a new MinIO client
func NewMinIOClient(ctx context.Context, config *MinIOConfig) (*MinIOClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("MinIO configuration is required")
	}
	if config.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if config.AccessKey == "" {
		return nil, fmt.Errorf("access key is required")
	}
	if config.SecretKey == "" {
		return nil, fmt.Errorf("secret key is required")
	}

	// Initialize minio client options
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, config.Token),
		Secure: config.Secure,
		Region: config.Region,
	}

	// Create minio client
	client, err := minio.New(config.Endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	return &MinIOClient{
		client:    client,
		endpoint:  config.Endpoint,
		accessKey: config.AccessKey,
		secretKey: config.SecretKey,
		region:    config.Region,
		bucket:    config.Bucket,
		secure:    config.Secure,
	}, nil
}

// ListObjects lists objects in a bucket with optional prefix
func (c *MinIOClient) ListObjects(ctx context.Context, prefix string, maxKeys int) ([]MinIOObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	objectCh := c.client.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	})

	objects := make([]MinIOObject, 0)
	count := 0

	for object := range objectCh {
		if object.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", object.Err)
		}

		objects = append(objects, MinIOObject{
			Key:          object.Key,
			Size:         object.Size,
			LastModified: object.LastModified,
			ETag:         object.ETag,
			ContentType:  object.ContentType,
		})

		count++
		if maxKeys > 0 && count >= maxKeys {
			break
		}
	}

	return objects, nil
}

// ListObjectsRecursive lists objects recursively with a prefix
func (c *MinIOClient) ListObjectsRecursive(ctx context.Context, prefix string) ([]MinIOObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	objectCh := c.client.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	objects := make([]MinIOObject, 0)

	for object := range objectCh {
		if object.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", object.Err)
		}

		objects = append(objects, MinIOObject{
			Key:          object.Key,
			Size:         object.Size,
			LastModified: object.LastModified,
			ETag:         object.ETag,
			ContentType:  object.ContentType,
		})
	}

	return objects, nil
}

// GetObject retrieves an object from MinIO
func (c *MinIOClient) GetObject(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("MinIO client is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("MinIO object key is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	obj, err := c.client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// GetObjectRange retrieves a range of bytes from an object
func (c *MinIOClient) GetObjectRange(ctx context.Context, key string, offset, length int64) ([]byte, error) {
	if offset < 0 {
		return nil, fmt.Errorf("object range offset must not be negative")
	}
	if length < 0 {
		return nil, fmt.Errorf("object range length must not be negative")
	}
	if length == 0 {
		return []byte{}, nil
	}
	maxInt64 := int64(^uint64(0) >> 1)
	if offset > maxInt64-(length-1) {
		return nil, fmt.Errorf("object range exceeds supported size")
	}
	maxInt := int64(int(^uint(0) >> 1))
	if length > maxInt {
		return nil, fmt.Errorf("object range length exceeds the platform limit")
	}

	opts := minio.GetObjectOptions{}
	opts.SetRange(offset, offset+length-1)

	obj, err := c.client.GetObject(ctx, c.bucket, key, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get object range: %w", err)
	}
	defer obj.Close()

	data := make([]byte, length)
	if _, err = io.ReadFull(obj, data); err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}

	return data, nil
}

// GetObjectMetadata retrieves object metadata without downloading the content
func (c *MinIOClient) GetObjectMetadata(ctx context.Context, key string) (*MinIOObjectMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	stat, err := c.client.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to stat object: %w", err)
	}

	// Convert http.Header to map[string]string
	metadataMap := make(map[string]string)
	for k, v := range stat.Metadata {
		if len(v) > 0 {
			metadataMap[k] = v[0]
		}
	}

	metadata := &MinIOObjectMetadata{
		Key:           key,
		ContentLength: stat.Size,
		ContentType:   stat.ContentType,
		LastModified:  stat.LastModified,
		ETag:          stat.ETag,
		Expires:       stat.Expires,
		Metadata:      metadataMap,
	}

	if stat.ContentType != "" {
		metadata.ContentEncoding = stat.Metadata.Get("Content-Encoding")
	}

	if stat.Metadata.Get("Cache-Control") != "" {
		metadata.CacheControl = stat.Metadata.Get("Cache-Control")
	}

	return metadata, nil
}

// PutObject uploads an object to MinIO
func (c *MinIOClient) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	opts := minio.PutObjectOptions{
		ContentType: contentType,
	}

	_, err := c.client.PutObject(ctx, c.bucket, key, bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	return nil
}

// DeleteObject deletes an object from MinIO
func (c *MinIOClient) DeleteObject(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := c.client.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

// DeleteObjects deletes multiple objects from MinIO
func (c *MinIOClient) DeleteObjects(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	objectCh := make(chan minio.ObjectInfo)

	// Send keys to channel
	go func() {
		defer close(objectCh)
		for _, key := range keys {
			objectCh <- minio.ObjectInfo{Key: key}
		}
	}()

	errorCh := c.client.RemoveObjects(ctx, c.bucket, objectCh, minio.RemoveObjectsOptions{})

	// Check for errors
	for err := range errorCh {
		if err.Err != nil {
			return fmt.Errorf("failed to delete object %s: %w", err.ObjectName, err.Err)
		}
	}

	return nil
}

// CopyObject copies an object within MinIO
func (c *MinIOClient) CopyObject(ctx context.Context, srcKey, destKey string) error {
	// Get the source object
	obj, err := c.client.GetObject(ctx, c.bucket, srcKey, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to get source object: %w", err)
	}
	defer obj.Close()

	// Get object info
	objInfo, err := obj.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source object: %w", err)
	}

	// Copy by uploading the content to the destination
	_, err = c.client.PutObject(ctx, c.bucket, destKey, obj, objInfo.Size, minio.PutObjectOptions{
		ContentType: objInfo.ContentType,
	})
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// PresignURL generates a presigned URL for temporary access
func (c *MinIOClient) PresignURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	presignedURL, err := c.client.PresignedGetObject(ctx, c.bucket, key, expiration, nil)
	if err != nil {
		return "", fmt.Errorf("failed to presign URL: %w", err)
	}

	return presignedURL.String(), nil
}

// ObjectExists checks if an object exists
func (c *MinIOClient) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		// Check if error is "not found"
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetObjectSize gets the size of an object without downloading it
func (c *MinIOClient) GetObjectSize(ctx context.Context, key string) (int64, error) {
	stat, err := c.client.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, err
	}
	return stat.Size, nil
}

// BucketExists checks if a bucket exists
func (c *MinIOClient) BucketExists(ctx context.Context, bucketName string) (bool, error) {
	exists, err := c.client.BucketExists(ctx, bucketName)
	if err != nil {
		return false, fmt.Errorf("failed to check bucket existence: %w", err)
	}
	return exists, nil
}

// MakeBucket creates a new bucket
func (c *MinIOClient) MakeBucket(ctx context.Context, bucketName string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{
		Region: c.region,
	})
	if err != nil {
		return fmt.Errorf("failed to make bucket: %w", err)
	}

	return nil
}

// RemoveBucket removes a bucket
func (c *MinIOClient) RemoveBucket(ctx context.Context, bucketName string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := c.client.RemoveBucket(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to remove bucket: %w", err)
	}

	return nil
}

// ListBuckets lists all buckets
func (c *MinIOClient) ListBuckets(ctx context.Context) ([]minio.BucketInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	buckets, err := c.client.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}

	return buckets, nil
}

// MinIOObject represents a MinIO object
type MinIOObject struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	ContentType  string
}

// MinIOObjectMetadata represents MinIO object metadata
type MinIOObjectMetadata struct {
	Key             string
	ContentLength   int64
	ContentType     string
	ContentEncoding string
	LastModified    time.Time
	ETag            string
	Expires         time.Time
	CacheControl    string
	Metadata        map[string]string
}

// GetBucketInfo returns bucket information
func (c *MinIOClient) GetBucketInfo(ctx context.Context) *MinIOBucketInfo {
	protocol := "http"
	if c.secure {
		protocol = "https"
	}

	return &MinIOBucketInfo{
		Name:     c.bucket,
		Endpoint: c.endpoint,
		Region:   c.region,
		URL:      fmt.Sprintf("%s://%s/%s", protocol, c.endpoint, c.bucket),
	}
}

// MinIOBucketInfo represents MinIO bucket information
type MinIOBucketInfo struct {
	Name     string
	Endpoint string
	Region   string
	URL      string
}

// SetBucketPolicy sets bucket policy
func (c *MinIOClient) SetBucketPolicy(ctx context.Context, policy string) error {
	return c.client.SetBucketPolicy(ctx, c.bucket, policy)
}

// GetBucketPolicy gets bucket policy
func (c *MinIOClient) GetBucketPolicy(ctx context.Context) (string, error) {
	return c.client.GetBucketPolicy(ctx, c.bucket)
}

// ListFilesByExtension lists files with a specific extension
func (c *MinIOClient) ListFilesByExtension(ctx context.Context, prefix, extension string) ([]MinIOObject, error) {
	objects, err := c.ListObjects(ctx, prefix, 0)
	if err != nil {
		return nil, err
	}

	var filtered []MinIOObject
	for _, obj := range objects {
		if hasExtension(obj.Key, extension) {
			filtered = append(filtered, obj)
		}
	}

	return filtered, nil
}

// ListFilesByExtensions lists files with any of the given extensions
func (c *MinIOClient) ListFilesByExtensions(ctx context.Context, prefix string, extensions []string) ([]MinIOObject, error) {
	objects, err := c.ListObjects(ctx, prefix, 0)
	if err != nil {
		return nil, err
	}

	var filtered []MinIOObject
	for _, obj := range objects {
		for _, ext := range extensions {
			if hasExtension(obj.Key, ext) {
				filtered = append(filtered, obj)
				break
			}
		}
	}

	return filtered, nil
}

// GetObjectReader gets a reader for an object
func (c *MinIOClient) GetObjectReader(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	return obj, nil
}
