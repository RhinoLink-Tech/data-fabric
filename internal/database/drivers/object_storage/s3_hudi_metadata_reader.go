package object_storage

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/table_formats"
)

type s3HudiMetadataClient interface {
	ListObjects(context.Context, string, int32) ([]S3Object, error)
	ListObjectsAll(context.Context, string) ([]S3Object, error)
	GetObject(context.Context, string) ([]byte, error)
}

// S3HudiMetadataReader reads standard Hudi table properties and the classic
// active timeline from S3. It deliberately does not claim row-level query support.
type S3HudiMetadataReader struct {
	client s3HudiMetadataClient
	bucket string
	parser *table_formats.HudiMetadataParser
}

func NewS3HudiMetadataReader(ctx context.Context, config *S3Config) (*S3HudiMetadataReader, error) {
	if config == nil {
		return nil, fmt.Errorf("S3 configuration is required")
	}
	if strings.TrimSpace(config.Bucket) == "" {
		return nil, fmt.Errorf("S3 bucket is required")
	}
	client, err := NewS3Client(ctx, config)
	if err != nil {
		return nil, err
	}
	return NewS3HudiMetadataReaderWithClient(client, config.Bucket), nil
}

func NewS3HudiMetadataReaderWithClient(client s3HudiMetadataClient, bucket string) *S3HudiMetadataReader {
	return &S3HudiMetadataReader{
		client: client,
		bucket: bucket,
		parser: table_formats.NewHudiMetadataParser(),
	}
}

func (r *S3HudiMetadataReader) TestConnection(ctx context.Context) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("S3 Hudi metadata reader is not configured")
	}
	_, err := r.client.ListObjects(ctx, "", 1)
	return err
}

func (r *S3HudiMetadataReader) GetTableMetadata(ctx context.Context, tablePath string) (*table_formats.HudiTableMetadata, error) {
	prefix, err := r.tablePrefix(tablePath)
	if err != nil {
		return nil, err
	}
	data, err := r.client.GetObject(ctx, joinObjectKey(prefix, ".hoodie/hoodie.properties"))
	if err != nil {
		return nil, fmt.Errorf("read Hudi hoodie.properties: %w", err)
	}
	metadata, err := r.parser.ParseHudiProperties(data)
	if err != nil {
		return nil, err
	}
	if metadata.HudiVersion != "" {
		version, err := strconv.Atoi(metadata.HudiVersion)
		if err != nil {
			return nil, fmt.Errorf("unsupported Hudi table format version %q", metadata.HudiVersion)
		}
		if version >= 8 {
			return nil, fmt.Errorf("Hudi table format version %d uses the LSM timeline layout; this reader supports classic timeline metadata only", version)
		}
	}
	return metadata, nil
}

func (r *S3HudiMetadataReader) GetCommits(ctx context.Context, tablePath string) ([]table_formats.HudiCommit, error) {
	objects, err := r.listClassicTimeline(ctx, tablePath)
	if err != nil {
		return nil, err
	}
	commits := make([]table_formats.HudiCommit, 0)
	for _, object := range objects {
		instant, action, completed := parseClassicHudiTimelineKey(object.Key)
		if !completed || !isHudiCommitAction(action) {
			continue
		}
		timestamp, err := r.parser.ParseInstantTime(instant)
		if err != nil {
			return nil, fmt.Errorf("invalid Hudi timeline instant in %q: %w", object.Key, err)
		}
		commits = append(commits, table_formats.HudiCommit{
			CommitTime:  instant,
			ActionType:  action,
			State:       "COMPLETED",
			TimestampMs: timestamp.UnixMilli(),
		})
	}
	sort.Slice(commits, func(i, j int) bool {
		return commits[i].CommitTime < commits[j].CommitTime
	})
	return commits, nil
}

func (r *S3HudiMetadataReader) GetSavepoints(ctx context.Context, tablePath string) ([]table_formats.HudiSavepoint, error) {
	objects, err := r.listClassicTimeline(ctx, tablePath)
	if err != nil {
		return nil, err
	}
	savepoints := make([]table_formats.HudiSavepoint, 0)
	for _, object := range objects {
		instant, action, completed := parseClassicHudiTimelineKey(object.Key)
		if completed && action == "savepoint" {
			savepoints = append(savepoints, table_formats.HudiSavepoint{
				SavepointTime: instant,
				CommitTime:    instant,
				Path:          object.Key,
			})
		}
	}
	sort.Slice(savepoints, func(i, j int) bool {
		return savepoints[i].SavepointTime < savepoints[j].SavepointTime
	})
	return savepoints, nil
}

func (r *S3HudiMetadataReader) ListPartitions(ctx context.Context, tablePath string) ([]table_formats.HudiPartition, error) {
	prefix, err := r.tablePrefix(tablePath)
	if err != nil {
		return nil, err
	}
	objects, err := r.client.ListObjectsAll(ctx, directoryPrefix(prefix))
	if err != nil {
		return nil, fmt.Errorf("list Hudi table objects: %w", err)
	}

	byPartition := make(map[string]*table_formats.HudiPartition)
	for _, object := range objects {
		relative := strings.TrimPrefix(object.Key, directoryPrefix(prefix))
		if strings.HasPrefix(relative, ".hoodie/") || !isHudiDataObject(relative) {
			continue
		}
		partitionPath := path.Dir(relative)
		if partitionPath == "." {
			partitionPath = ""
		}
		partition := byPartition[partitionPath]
		if partition == nil {
			partition = &table_formats.HudiPartition{
				Path:   partitionPath,
				Values: parseHudiPartitionValues(partitionPath),
			}
			byPartition[partitionPath] = partition
		}
		partition.FileCount++
		partition.SizeBytes += object.Size
	}

	partitions := make([]table_formats.HudiPartition, 0, len(byPartition))
	for _, partition := range byPartition {
		partitions = append(partitions, *partition)
	}
	sort.Slice(partitions, func(i, j int) bool { return partitions[i].Path < partitions[j].Path })
	return partitions, nil
}

func (r *S3HudiMetadataReader) listClassicTimeline(ctx context.Context, tablePath string) ([]S3Object, error) {
	metadata, err := r.GetTableMetadata(ctx, tablePath)
	if err != nil {
		return nil, err
	}
	prefix, err := r.tablePrefix(tablePath)
	if err != nil {
		return nil, err
	}
	timelineDir := metadata.Properties["hoodie.table.timeline.path"]
	if timelineDir != "" {
		clean := path.Clean(timelineDir)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			return nil, fmt.Errorf("invalid Hudi timeline path %q", timelineDir)
		}
		for _, segment := range strings.Split(clean, "/") {
			if segment == "." || segment == ".." {
				return nil, fmt.Errorf("invalid Hudi timeline path %q", timelineDir)
			}
		}
		prefix = joinObjectKey(prefix, ".hoodie/"+clean)
	} else {
		prefix = joinObjectKey(prefix, ".hoodie")
	}
	return r.client.ListObjectsAll(ctx, directoryPrefix(prefix))
}

func (r *S3HudiMetadataReader) tablePrefix(tablePath string) (string, error) {
	if r == nil || r.client == nil {
		return "", fmt.Errorf("S3 Hudi metadata reader is not configured")
	}
	if strings.TrimSpace(r.bucket) == "" {
		return "", fmt.Errorf("S3 bucket is required")
	}
	trimmed := strings.TrimSpace(tablePath)
	if strings.HasPrefix(trimmed, "s3://") {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Host != r.bucket || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("Hudi table URI must target configured bucket %q", r.bucket)
		}
		trimmed = parsed.Path
	}
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", fmt.Errorf("Hudi table path is required")
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid Hudi table path %q", tablePath)
		}
	}
	return trimmed, nil
}

func joinObjectKey(prefix, suffix string) string {
	prefix = strings.Trim(prefix, "/")
	suffix = strings.Trim(suffix, "/")
	if prefix == "" {
		return suffix
	}
	return prefix + "/" + suffix
}

func directoryPrefix(prefix string) string {
	if prefix == "" {
		return ""
	}
	return strings.Trim(prefix, "/") + "/"
}

func parseClassicHudiTimelineKey(key string) (instant, action string, completed bool) {
	name := path.Base(key)
	parts := strings.Split(name, ".")
	if len(parts) < 2 || len(parts) > 2 {
		return "", "", false
	}
	instant, action = parts[0], parts[1]
	if len(instant) != 14 && len(instant) != 17 {
		return "", "", false
	}
	return instant, action, true
}

func isHudiCommitAction(action string) bool {
	switch action {
	case "commit", "deltacommit", "replacecommit":
		return true
	default:
		return false
	}
}

func isHudiDataObject(relative string) bool {
	name := path.Base(relative)
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return false
	}
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".parquet") || strings.HasSuffix(lower, ".orc") || strings.Contains(lower, ".log.")
}

func parseHudiPartitionValues(partitionPath string) map[string]interface{} {
	values := make(map[string]interface{})
	if partitionPath == "" {
		return values
	}
	for _, segment := range strings.Split(partitionPath, "/") {
		parts := strings.SplitN(segment, "=", 2)
		if len(parts) == 2 && parts[0] != "" {
			values[parts[0]] = parts[1]
		}
	}
	return values
}
