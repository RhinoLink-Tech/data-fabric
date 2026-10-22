package object_storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// S3Client wraps AWS S3 client with convenience methods
type S3Client struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	region        string
	config        *S3Config
}

// S3Config holds S3 configuration
type S3Config struct {
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	SessionToken   string // For temporary credentials
	RoleARN        string // For IAM role assumption
	ExternalID     string // For IAM role assumption
	EndpointURL    string // For S3-compatible services (MinIO, LocalStack)
	DisableSSL     bool
	ForcePathStyle bool // Required for MinIO
	MaxRetries     int
	Timeout        time.Duration
}

// NewS3Client creates a new S3 client
func NewS3Client(ctx context.Context, s3Config *S3Config) (*S3Client, error) {
	if s3Config.Region == "" {
		return nil, fmt.Errorf("region is required")
	}

	// Load AWS configuration
	cfgOpts := []func(*config.LoadOptions) error{
		config.WithRegion(s3Config.Region),
	}

	// Set credentials if provided
	if s3Config.AccessKey != "" && s3Config.SecretKey != "" {
		cfgOpts = append(cfgOpts, config.WithCredentialsProvider(
			aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
				return aws.Credentials{
					AccessKeyID:     s3Config.AccessKey,
					SecretAccessKey: s3Config.SecretKey,
					SessionToken:    s3Config.SessionToken,
				}, nil
			}),
		))
	}

	if s3Config.MaxRetries > 0 {
		cfgOpts = append(cfgOpts, config.WithRetryMaxAttempts(s3Config.MaxRetries))
	}

	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Assume IAM role if specified
	if s3Config.RoleARN != "" {
		// Create STS client for role assumption
		stsSvc := sts.NewFromConfig(cfg)
		stsRoleCredentials := stscreds.NewAssumeRoleProvider(stsSvc, s3Config.RoleARN, func(o *stscreds.AssumeRoleOptions) {
			if s3Config.ExternalID != "" {
				o.ExternalID = aws.String(s3Config.ExternalID)
			}
		})
		cfg.Credentials = stsRoleCredentials
	}

	// Create S3 client
	clientOpts := []func(*s3.Options){}

	if s3Config.EndpointURL != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(s3Config.EndpointURL)
		})
	}

	if s3Config.ForcePathStyle {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(cfg, clientOpts...)

	return &S3Client{
		client:        client,
		presignClient: s3.NewPresignClient(client),
		region:        s3Config.Region,
		config:        s3Config,
	}, nil
}

// ListObjects lists objects in a bucket with optional prefix
func (c *S3Client) ListObjects(ctx context.Context, prefix string, maxKeys int32) ([]S3Object, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(c.config.Bucket),
	}

	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}

	if maxKeys > 0 {
		input.MaxKeys = aws.Int32(maxKeys)
	}

	result, err := c.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	objects := make([]S3Object, 0, len(result.Contents))
	for _, obj := range result.Contents {
		objects = append(objects, S3Object{
			Key:          aws.ToString(obj.Key),
			Size:         aws.ToInt64(obj.Size),
			LastModified: aws.ToTime(obj.LastModified),
			ETag:         aws.ToString(obj.ETag),
			StorageClass: string(obj.StorageClass),
		})
	}

	return objects, nil
}

// ListObjectsAll follows every ListObjectsV2 continuation token and returns
// all objects matching the prefix. Callers that need a stable view should pin
// their own format version/cursor; S3 listing is not a table snapshot.
func (c *S3Client) ListObjectsAll(ctx context.Context, prefix string) ([]S3Object, error) {
	input := &s3.ListObjectsV2Input{Bucket: aws.String(c.config.Bucket)}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	paginator := s3.NewListObjectsV2Paginator(c.client, input, func(options *s3.ListObjectsV2PaginatorOptions) {
		options.Limit = 1000
	})

	objects := make([]S3Object, 0)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list S3 objects: %w", err)
		}
		for _, object := range page.Contents {
			objects = append(objects, S3Object{
				Key:          aws.ToString(object.Key),
				Size:         aws.ToInt64(object.Size),
				LastModified: aws.ToTime(object.LastModified),
				ETag:         aws.ToString(object.ETag),
				StorageClass: string(object.StorageClass),
			})
			if len(objects) > 1_000_000 {
				return nil, fmt.Errorf("S3 object listing exceeded the 1000000 object safety limit")
			}
		}
	}
	return objects, nil
}

// ListObjectsWithDelimiter lists objects with delimiter for common prefixes
func (c *S3Client) ListObjectsWithDelimiter(ctx context.Context, prefix, delimiter string) (*S3ListResult, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(c.config.Bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String(delimiter),
	}

	result, err := c.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	s3Result := &S3ListResult{
		Objects:  make([]S3Object, 0, len(result.Contents)),
		Prefixes: make([]string, 0, len(result.CommonPrefixes)),
	}

	for _, obj := range result.Contents {
		s3Result.Objects = append(s3Result.Objects, S3Object{
			Key:          aws.ToString(obj.Key),
			Size:         aws.ToInt64(obj.Size),
			LastModified: aws.ToTime(obj.LastModified),
			ETag:         aws.ToString(obj.ETag),
			StorageClass: string(obj.StorageClass),
		})
	}

	for _, prefix := range result.CommonPrefixes {
		s3Result.Prefixes = append(s3Result.Prefixes, aws.ToString(prefix.Prefix))
	}

	s3Result.IsTruncated = aws.ToBool(result.IsTruncated)
	s3Result.NextContinuationToken = aws.ToString(result.NextContinuationToken)

	return s3Result, nil
}

// GetObject retrieves an object from S3
func (c *S3Client) GetObject(ctx context.Context, key string) ([]byte, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	}

	result, err := c.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}

	return data, nil
}

// GetObjectRange retrieves a range of bytes from an object
func (c *S3Client) GetObjectRange(ctx context.Context, key string, start, end int64) ([]byte, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
	}

	result, err := c.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get object range: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object data: %w", err)
	}

	return data, nil
}

// GetObjectMetadata retrieves object metadata without downloading the content
func (c *S3Client) GetObjectMetadata(ctx context.Context, key string) (*S3ObjectMetadata, error) {
	input := &s3.HeadObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	}

	result, err := c.client.HeadObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to head object: %w", err)
	}

	metadata := &S3ObjectMetadata{
		Key:           key,
		ContentLength: aws.ToInt64(result.ContentLength),
		ContentType:   aws.ToString(result.ContentType),
		LastModified:  aws.ToTime(result.LastModified),
		ETag:          aws.ToString(result.ETag),
		Metadata:      result.Metadata,
	}

	if result.ContentEncoding != nil {
		metadata.ContentEncoding = aws.ToString(result.ContentEncoding)
	}

	if result.CacheControl != nil {
		metadata.CacheControl = aws.ToString(result.CacheControl)
	}

	return metadata, nil
}

// PutObject uploads an object to S3
func (c *S3Client) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	_, err := c.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}

	return nil
}

// DeleteObject deletes an object from S3
func (c *S3Client) DeleteObject(ctx context.Context, key string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	}

	_, err := c.client.DeleteObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

// DeleteObjects deletes multiple objects from S3
func (c *S3Client) DeleteObjects(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	objectIds := make([]types.ObjectIdentifier, len(keys))
	for i, key := range keys {
		objectIds[i] = types.ObjectIdentifier{Key: aws.String(key)}
	}

	input := &s3.DeleteObjectsInput{
		Bucket: aws.String(c.config.Bucket),
		Delete: &types.Delete{Objects: objectIds},
	}

	_, err := c.client.DeleteObjects(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete objects: %w", err)
	}

	return nil
}

// CopyObject copies an object within S3
func (c *S3Client) CopyObject(ctx context.Context, srcKey, destKey string) error {
	src := fmt.Sprintf("%s/%s", c.config.Bucket, srcKey)
	input := &s3.CopyObjectInput{
		Bucket:     aws.String(c.config.Bucket),
		CopySource: aws.String(src),
		Key:        aws.String(destKey),
	}

	_, err := c.client.CopyObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// PresignURL generates a presigned URL for temporary access
func (c *S3Client) PresignURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	}

	presignResult, err := c.presignClient.PresignGetObject(ctx, input, s3.WithPresignExpires(expiration))
	if err != nil {
		return "", fmt.Errorf("failed to presign URL: %w", err)
	}

	return presignResult.URL, nil
}

// ObjectExists checks if an object exists
func (c *S3Client) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := c.GetObjectMetadata(ctx, key)
	if err != nil {
		var nsk *types.NotFound
		if _, ok := err.(*types.NotFound); ok || err == nsk {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetObjectReader gets a reader for an object
func (c *S3Client) GetObjectReader(ctx context.Context, key string) (io.ReadCloser, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	}

	result, err := c.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}

	return result.Body, nil
}

// GetObjectSize gets the size of an object without downloading it
func (c *S3Client) GetObjectSize(ctx context.Context, key string) (int64, error) {
	metadata, err := c.GetObjectMetadata(ctx, key)
	if err != nil {
		return 0, err
	}
	return metadata.ContentLength, nil
}

// GetBucketLocation gets the bucket location
func (c *S3Client) GetBucketLocation(ctx context.Context) (string, error) {
	input := &s3.GetBucketLocationInput{
		Bucket: aws.String(c.config.Bucket),
	}

	result, err := c.client.GetBucketLocation(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to get bucket location: %w", err)
	}

	location := string(result.LocationConstraint)
	if location == "" {
		location = "us-east-1" // Default region
	}

	return location, nil
}

// GetBucketInfo retrieves bucket information
func (c *S3Client) GetBucketInfo(ctx context.Context) (*S3BucketInfo, error) {
	location, err := c.GetBucketLocation(ctx)
	if err != nil {
		return nil, err
	}

	return &S3BucketInfo{
		Name:     c.config.Bucket,
		Region:   location,
		Endpoint: c.config.EndpointURL,
	}, nil
}

// S3Object represents an S3 object
type S3Object struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	StorageClass string
}

// S3ListResult represents the result of listing objects
type S3ListResult struct {
	Objects               []S3Object
	Prefixes              []string
	IsTruncated           bool
	NextContinuationToken string
}

// S3ObjectMetadata represents S3 object metadata
type S3ObjectMetadata struct {
	Key             string
	ContentLength   int64
	ContentType     string
	ContentEncoding string
	LastModified    time.Time
	ETag            string
	CacheControl    string
	Metadata        map[string]string
}

// S3BucketInfo represents S3 bucket information
type S3BucketInfo struct {
	Name     string
	Region   string
	Endpoint string
}

// ParseS3URI parses an S3 URI (s3://bucket/key/path)
func ParseS3URI(uri string) (bucket, key string, err error) {
	if len(uri) < 5 || uri[:5] != "s3://" {
		return "", "", fmt.Errorf("invalid S3 URI: %s", uri)
	}

	withoutPrefix := uri[5:]
	slashIdx := -1
	for i, c := range withoutPrefix {
		if c == '/' {
			slashIdx = i
			break
		}
	}

	if slashIdx == -1 {
		return withoutPrefix, "", nil
	}

	bucket = withoutPrefix[:slashIdx]
	key = withoutPrefix[slashIdx+1:]

	return bucket, key, nil
}

// BuildS3URI builds an S3 URI from bucket and key
func BuildS3URI(bucket, key string) string {
	if key == "" {
		return fmt.Sprintf("s3://%s", bucket)
	}
	return fmt.Sprintf("s3://%s/%s", bucket, key)
}

// GetObjectByURI gets an object using S3 URI
func (c *S3Client) GetObjectByURI(ctx context.Context, uri string) ([]byte, error) {
	bucket, key, err := ParseS3URI(uri)
	if err != nil {
		return nil, err
	}

	// Use bucket from URI if different from client config
	if bucket != c.config.Bucket {
		return nil, fmt.Errorf("bucket mismatch: URI bucket %s != client bucket %s", bucket, c.config.Bucket)
	}

	return c.GetObject(ctx, key)
}

// ListFilesByExtension lists files with a specific extension
func (c *S3Client) ListFilesByExtension(ctx context.Context, prefix, extension string) ([]S3Object, error) {
	objects, err := c.ListObjects(ctx, prefix, 0)
	if err != nil {
		return nil, err
	}

	var filtered []S3Object
	for _, obj := range objects {
		if hasExtension(obj.Key, extension) {
			filtered = append(filtered, obj)
		}
	}

	return filtered, nil
}

// hasExtension checks if a filename has the given extension
func hasExtension(filename, extension string) bool {
	if len(extension) > 0 && extension[0] != '.' {
		extension = "." + extension
	}
	return len(filename) > len(extension) && filename[len(filename)-len(extension):] == extension
}

// ListFilesByExtensions lists files with any of the given extensions
func (c *S3Client) ListFilesByExtensions(ctx context.Context, prefix string, extensions []string) ([]S3Object, error) {
	objects, err := c.ListObjects(ctx, prefix, 0)
	if err != nil {
		return nil, err
	}

	var filtered []S3Object
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
