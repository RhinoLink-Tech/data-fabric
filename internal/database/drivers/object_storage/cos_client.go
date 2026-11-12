package object_storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bytes"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// COSClient wraps Tencent Cloud COS client
type COSClient struct {
	client *cos.Client
	bucket string
	config *COSConfig
}

// COSConfig holds Tencent Cloud COS configuration
type COSConfig struct {
	SecretID    string
	SecretKey   string
	Region      string // e.g., ap-guangzhou
	BucketName  string
	HTTPS       bool
	EndpointURL string // Optional HTTP(S) endpoint override, primarily for compatible gateways.
}

// NewCOSClient creates a new Tencent Cloud COS client
func NewCOSClient(ctx context.Context, config *COSConfig) (*COSClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("COS configuration is required")
	}
	if strings.TrimSpace(config.SecretID) == "" {
		return nil, fmt.Errorf("secret ID is required")
	}
	if strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("secret key is required")
	}
	if strings.TrimSpace(config.Region) == "" {
		return nil, fmt.Errorf("region is required")
	}
	if strings.TrimSpace(config.BucketName) == "" {
		return nil, fmt.Errorf("bucket name is required")
	}

	var bucketURL *url.URL
	var err error
	if strings.TrimSpace(config.EndpointURL) != "" {
		bucketURL, err = url.Parse(strings.TrimSpace(config.EndpointURL))
		if err != nil || bucketURL.Host == "" || (bucketURL.Scheme != "http" && bucketURL.Scheme != "https") ||
			(bucketURL.Path != "" && bucketURL.Path != "/") || bucketURL.User != nil ||
			bucketURL.RawQuery != "" || bucketURL.Fragment != "" {
			return nil, fmt.Errorf("COS endpoint URL must be an HTTP(S) origin without credentials, path, query, or fragment")
		}
	} else {
		bucketURL, err = cos.NewBucketURL(config.BucketName, config.Region, config.HTTPS)
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket URL: %w", err)
		}
	}

	// The SDK needs the AuthorizationTransport to sign each COS request.
	client := cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, &http.Client{
		Timeout: 30 * time.Second,
		Transport: &cos.AuthorizationTransport{
			SecretID: config.SecretID, SecretKey: config.SecretKey,
		},
	})

	return &COSClient{
		client: client,
		bucket: config.BucketName,
		config: config,
	}, nil
}

// ListObjects lists objects in a bucket
func (c *COSClient) ListObjects(ctx context.Context, prefix string, maxKeys int) ([]COSObject, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("COS client is not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	listOpts := &cos.BucketGetOptions{
		Prefix:  prefix,
		MaxKeys: maxKeys,
	}

	resp, _, err := c.client.Bucket.Get(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	objects := make([]COSObject, 0, len(resp.Contents))
	for _, obj := range resp.Contents {
		parsedTime, _ := time.Parse(time.RFC3339, obj.LastModified)
		objects = append(objects, COSObject{
			Key:          obj.Key,
			Size:         int64(obj.Size),
			LastModified: parsedTime,
			ETag:         obj.ETag,
		})
	}

	return objects, nil
}

// GetObject retrieves an object from COS
func (c *COSClient) GetObject(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("COS client is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("COS object key is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := c.client.Object.Get(ctx, key, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return data, nil
}

// GetObjectLimited reads at most maxBytes plus one sentinel byte so callers can
// enforce memory bounds even when the object metadata advertises a smaller size.
func (c *COSClient) GetObjectLimited(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == int64(^uint64(0)>>1) {
		return nil, fmt.Errorf("maximum COS object size must be positive and representable")
	}
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("COS client is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("COS object key is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := c.client.Object.Get(ctx, key, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("COS object exceeds configured maximum of %d bytes", maxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// GetObjectMetadata retrieves object metadata
func (c *COSClient) GetObjectMetadata(ctx context.Context, key string) (*COSObjectMetadata, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("COS client is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("COS object key is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := c.client.Object.Head(ctx, key, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to head object: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	metadata := &COSObjectMetadata{
		Key:           key,
		ContentLength: int64(resp.ContentLength),
		ContentType:   resp.Header.Get("Content-Type"),
		ETag:          resp.Header.Get("ETag"),
	}

	if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
		parsedTime, _ := time.Parse(time.RFC1123, lastModified)
		metadata.LastModified = parsedTime
	}

	return metadata, nil
}

// PutObject uploads an object to COS
func (c *COSClient) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	opts := &cos.ObjectPutOptions{}
	if contentType != "" {
		opts.ObjectPutHeaderOptions = &cos.ObjectPutHeaderOptions{
			ContentType: contentType,
		}
	}

	_, err := c.client.Object.Put(ctx, key, bytes.NewReader(data), opts)
	if err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	return nil
}

// DeleteObject deletes an object from COS
func (c *COSClient) DeleteObject(ctx context.Context, key string) error {
	_, err := c.client.Object.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

// CopyObject copies an object within COS
func (c *COSClient) CopyObject(ctx context.Context, srcKey, destKey string) error {
	sourceURL := fmt.Sprintf("%s.cos.ap-%s.myqcloud.com/%s", c.bucket, c.config.Region, srcKey)

	_, _, err := c.client.Object.Copy(ctx, destKey, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// PresignURL generates a presigned URL for temporary access
func (c *COSClient) PresignURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	presignedURL, err := c.client.Object.GetPresignedURL(ctx, http.MethodGet, key, c.config.SecretID, c.config.SecretKey, expiration, nil)
	if err != nil {
		return "", fmt.Errorf("failed to presign URL: %w", err)
	}

	return presignedURL.String(), nil
}

// ObjectExists checks if an object exists
func (c *COSClient) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.Object.Head(ctx, key, nil)
	if err != nil {
		// Check if error is "not found"
		if cos.IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetObjectSize gets the size of an object
func (c *COSClient) GetObjectSize(ctx context.Context, key string) (int64, error) {
	metadata, err := c.GetObjectMetadata(ctx, key)
	if err != nil {
		return 0, err
	}
	return metadata.ContentLength, nil
}

// COSObject represents a COS object
type COSObject struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

// COSObjectMetadata represents COS object metadata
type COSObjectMetadata struct {
	Key             string
	ContentLength   int64
	ContentType     string
	ContentEncoding string
	LastModified    time.Time
	ETag            string
	CacheControl    string
}

// ListFilesByExtension lists files with a specific extension
func (c *COSClient) ListFilesByExtension(ctx context.Context, prefix, extension string) ([]COSObject, error) {
	objects, err := c.ListObjects(ctx, prefix, 1000)
	if err != nil {
		return nil, err
	}

	var filtered []COSObject
	for _, obj := range objects {
		if hasExtension(obj.Key, extension) {
			filtered = append(filtered, obj)
		}
	}

	return filtered, nil
}

// GetBucketInfo returns bucket information
func (c *COSClient) GetBucketInfo(ctx context.Context) *COSBucketInfo {
	endpoint := strings.TrimSpace(c.config.EndpointURL)
	if endpoint == "" {
		protocol := "http"
		if c.config.HTTPS {
			protocol = "https"
		}
		endpoint = fmt.Sprintf("%s://%s.cos.%s.myqcloud.com", protocol, c.bucket, c.config.Region)
	}
	return &COSBucketInfo{
		Name:   c.bucket,
		Region: c.config.Region,
		URL:    endpoint,
	}
}

// COSBucketInfo represents COS bucket information
type COSBucketInfo struct {
	Name   string
	Region string
	URL    string
}
