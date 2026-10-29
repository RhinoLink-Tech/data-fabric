package object_storage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxObjectDeltaLogObjects = 1000
	maxObjectDeltaDataFiles  = 256
	maxObjectDeltaTableBytes = 64 << 20
	maxObjectDeltaRows       = 100_000
)

var deltaCommitName = regexp.MustCompile(`^(\d{20})\.json$`)

type deltaListedObject struct {
	key  string
	size int64
}

type objectDeltaFile struct {
	path string
	size int64
}

type objectDeltaSnapshot struct {
	version int64
	files   []objectDeltaFile
}

func loadObjectDeltaSnapshot(
	ctx context.Context,
	tablePath string,
	list func(context.Context, string, int) ([]deltaListedObject, error),
	get func(context.Context, string) ([]byte, error),
) (*objectDeltaSnapshot, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tablePath, err := cleanObjectDeltaTablePath(tablePath)
	if err != nil {
		return nil, err
	}
	logPrefix := path.Join(tablePath, "_delta_log") + "/"
	objects, err := list(ctx, logPrefix, maxObjectDeltaLogObjects)
	if err != nil {
		return nil, fmt.Errorf("list Delta transaction log: %w", err)
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("Delta transaction log is empty at %q", logPrefix)
	}
	if len(objects) >= maxObjectDeltaLogObjects {
		return nil, fmt.Errorf("Delta transaction log has at least %d objects; this adapter supports fewer than %d log objects", len(objects), maxObjectDeltaLogObjects)
	}

	commits := make(map[int64]string)
	var latest int64 = -1
	for _, object := range objects {
		if !strings.HasPrefix(object.key, logPrefix) {
			continue
		}
		name := strings.TrimPrefix(object.key, logPrefix)
		if strings.Contains(name, "checkpoint") || strings.HasSuffix(name, ".parquet") {
			return nil, fmt.Errorf("Delta checkpoint %q is not supported by this adapter", name)
		}
		match := deltaCommitName.FindStringSubmatch(name)
		if len(match) != 2 {
			continue
		}
		version, parseErr := strconv.ParseInt(match[1], 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid Delta commit version %q: %w", name, parseErr)
		}
		if _, exists := commits[version]; exists {
			return nil, fmt.Errorf("duplicate Delta commit version %d", version)
		}
		commits[version] = object.key
		if version > latest {
			latest = version
		}
	}
	if latest < 0 {
		return nil, fmt.Errorf("no JSON Delta commit files found under %q", logPrefix)
	}

	active := make(map[string]objectDeltaFile)
	var hasProtocol, hasMetadata bool
	for version := int64(0); version <= latest; version++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, ok := commits[version]
		if !ok {
			return nil, fmt.Errorf("Delta transaction log is incomplete: missing version %d", version)
		}
		data, err := get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("read Delta commit %d: %w", version, err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if err := applyObjectDeltaAction(line, tablePath, active, &hasProtocol, &hasMetadata); err != nil {
				return nil, fmt.Errorf("Delta commit %d line %d: %w", version, lineNumber, err)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read Delta commit %d: %w", version, err)
		}
	}
	if !hasProtocol {
		return nil, errors.New("Delta snapshot has no supported protocol action")
	}
	if !hasMetadata {
		return nil, errors.New("Delta snapshot has no Parquet table metadata")
	}
	if len(active) == 0 {
		return nil, errors.New("empty Delta tables are not supported because no Parquet schema is available")
	}
	if len(active) > maxObjectDeltaDataFiles {
		return nil, fmt.Errorf("Delta snapshot has %d active files; the adapter limit is %d", len(active), maxObjectDeltaDataFiles)
	}

	files := make([]objectDeltaFile, 0, len(active))
	var totalBytes int64
	for _, file := range active {
		totalBytes += file.size
		files = append(files, file)
	}
	if totalBytes > maxObjectDeltaTableBytes {
		return nil, fmt.Errorf("Delta snapshot declares %d data bytes; the adapter limit is %d bytes", totalBytes, maxObjectDeltaTableBytes)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return &objectDeltaSnapshot{version: latest, files: files}, nil
}

func applyObjectDeltaAction(
	line string,
	tablePath string,
	active map[string]objectDeltaFile,
	hasProtocol *bool,
	hasMetadata *bool,
) error {
	var actions map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &actions); err != nil {
		return fmt.Errorf("invalid action JSON: %w", err)
	}
	if len(actions) != 1 {
		return errors.New("each transaction-log line must contain exactly one action")
	}
	for kind, raw := range actions {
		switch kind {
		case "protocol":
			var protocol struct {
				MinReaderVersion int      `json:"minReaderVersion"`
				ReaderFeatures   []string `json:"readerFeatures"`
			}
			if err := json.Unmarshal(raw, &protocol); err != nil {
				return fmt.Errorf("decode protocol action: %w", err)
			}
			if protocol.MinReaderVersion < 1 || protocol.MinReaderVersion > 1 || len(protocol.ReaderFeatures) > 0 {
				return fmt.Errorf("unsupported Delta reader protocol version %d or reader features %v", protocol.MinReaderVersion, protocol.ReaderFeatures)
			}
			*hasProtocol = true
		case "metaData":
			if !*hasProtocol {
				return errors.New("Delta metadata appeared before a supported protocol action")
			}
			var metadata struct {
				Format struct {
					Provider string `json:"provider"`
				} `json:"format"`
				PartitionColumns []string `json:"partitionColumns"`
			}
			if err := json.Unmarshal(raw, &metadata); err != nil {
				return fmt.Errorf("decode metadata action: %w", err)
			}
			if !strings.EqualFold(metadata.Format.Provider, "parquet") {
				return fmt.Errorf("Delta provider %q is not supported; expected parquet", metadata.Format.Provider)
			}
			if len(metadata.PartitionColumns) != 0 {
				return errors.New("partitioned Delta tables are not supported")
			}
			*hasMetadata = true
		case "add":
			if !*hasProtocol || !*hasMetadata {
				return errors.New("Delta data file appeared before protocol and Parquet metadata")
			}
			var add struct {
				Path            string                 `json:"path"`
				Size            int64                  `json:"size"`
				PartitionValues map[string]interface{} `json:"partitionValues"`
				DeletionVector  json.RawMessage        `json:"deletionVector"`
				Stats           json.RawMessage        `json:"stats"`
			}
			if err := json.Unmarshal(raw, &add); err != nil {
				return fmt.Errorf("decode add action: %w", err)
			}
			if len(add.PartitionValues) > 0 {
				return errors.New("partitioned Delta data files are not supported")
			}
			if len(add.DeletionVector) > 0 && string(add.DeletionVector) != "null" {
				return errors.New("Delta deletion vectors are not supported")
			}
			if add.Size < 0 {
				return errors.New("Delta add action has a negative file size")
			}
			key, err := resolveObjectDeltaDataPath(tablePath, add.Path)
			if err != nil {
				return err
			}
			active[key] = objectDeltaFile{path: key, size: add.Size}
		case "remove":
			if !*hasProtocol || !*hasMetadata {
				return errors.New("Delta remove action appeared before protocol and metadata")
			}
			var remove struct {
				Path           string          `json:"path"`
				DeletionVector json.RawMessage `json:"deletionVector"`
			}
			if err := json.Unmarshal(raw, &remove); err != nil {
				return fmt.Errorf("decode remove action: %w", err)
			}
			if len(remove.DeletionVector) > 0 && string(remove.DeletionVector) != "null" {
				return errors.New("Delta deletion vectors are not supported")
			}
			key, err := resolveObjectDeltaDataPath(tablePath, remove.Path)
			if err != nil {
				return err
			}
			delete(active, key)
		case "commitInfo", "txn":
			// These actions do not alter the visible data-file set.
		default:
			return fmt.Errorf("Delta action %q is not supported", kind)
		}
	}
	return nil
}

func cleanObjectDeltaTablePath(value string) (string, error) {
	value = strings.Trim(value, "/")
	if value == "" || strings.Contains(value, "\\") {
		return "", errors.New("Delta table_path must be a non-empty object-store path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", errors.New("Delta table_path contains an invalid path segment")
		}
	}
	return value, nil
}

func resolveObjectDeltaDataPath(tablePath, rawPath string) (string, error) {
	decoded, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", fmt.Errorf("invalid Delta data path %q: %w", rawPath, err)
	}
	if decoded == "" || strings.Contains(decoded, "\\") || strings.Contains(decoded, "://") || strings.HasPrefix(decoded, "/") {
		return "", fmt.Errorf("Delta data path %q must be relative to the table root", rawPath)
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", fmt.Errorf("Delta data path %q contains an invalid path segment", rawPath)
		}
	}
	key := path.Join(tablePath, decoded)
	if !strings.HasPrefix(key, tablePath+"/") {
		return "", fmt.Errorf("Delta data path %q escapes the table root", rawPath)
	}
	return key, nil
}

func readObjectDeltaRows(
	ctx context.Context,
	snapshot *objectDeltaSnapshot,
	batchSize int,
	maxBytes int64,
	get func(context.Context, string) ([]byte, error),
) (*ParquetSchema, []map[string]interface{}, error) {
	if snapshot == nil || len(snapshot.files) == 0 {
		return nil, nil, errors.New("Delta snapshot contains no active Parquet data files")
	}
	if batchSize <= 0 {
		batchSize = 1000
	}
	schema, rows, _, err := readObjectDeltaFiles(ctx, snapshot.files, batchSize, maxBytes, get, true)
	return schema, rows, err
}

func readObjectDeltaSchema(
	ctx context.Context,
	snapshot *objectDeltaSnapshot,
	batchSize int,
	get func(context.Context, string) ([]byte, error),
) (*ParquetSchema, error) {
	if snapshot == nil || len(snapshot.files) == 0 {
		return nil, errors.New("Delta snapshot contains no active Parquet data files")
	}
	schema, _, _, err := readObjectDeltaFiles(ctx, snapshot.files[:1], batchSize, maxObjectDeltaTableBytes, get, false)
	return schema, err
}

func readObjectDeltaFiles(
	ctx context.Context,
	files []objectDeltaFile,
	batchSize int,
	maxBytes int64,
	get func(context.Context, string) ([]byte, error),
	readRows bool,
) (*ParquetSchema, []map[string]interface{}, int64, error) {
	var commonSchema *ParquetSchema
	rows := make([]map[string]interface{}, 0)
	var bytesRead int64
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, bytesRead, err
		}
		data, err := get(ctx, file.path)
		if err != nil {
			return nil, nil, bytesRead, fmt.Errorf("read Delta data file %q: %w", file.path, err)
		}
		bytesRead += int64(len(data))
		if bytesRead > maxBytes {
			return nil, nil, bytesRead, fmt.Errorf("Delta data exceeds the %d-byte adapter limit", maxBytes)
		}
		reader, err := newParquetFileReader(newParquetMemoryFile(file.path, data), int64(batchSize))
		if err != nil {
			return nil, nil, bytesRead, fmt.Errorf("open Delta Parquet file %q: %w", file.path, err)
		}
		schema, err := reader.GetSchema()
		if err != nil {
			_ = reader.Close()
			return nil, nil, bytesRead, fmt.Errorf("read Delta Parquet schema %q: %w", file.path, err)
		}
		if commonSchema == nil {
			commonSchema = schema
		} else if !reflect.DeepEqual(commonSchema.Columns, schema.Columns) {
			_ = reader.Close()
			return nil, nil, bytesRead, fmt.Errorf("Delta data file %q has a schema that differs from the snapshot", file.path)
		}
		if readRows {
			for {
				if err := ctx.Err(); err != nil {
					_ = reader.Close()
					return nil, nil, bytesRead, err
				}
				batch, err := reader.ReadNumRows(batchSize)
				if err != nil {
					_ = reader.Close()
					return nil, nil, bytesRead, fmt.Errorf("decode Delta Parquet file %q: %w", file.path, err)
				}
				if len(batch) == 0 {
					break
				}
				if len(rows)+len(batch) > maxObjectDeltaRows {
					_ = reader.Close()
					return nil, nil, bytesRead, fmt.Errorf("Delta snapshot exceeds the %d-row adapter limit", maxObjectDeltaRows)
				}
				rows = append(rows, batch...)
			}
		}
		if err := reader.Close(); err != nil {
			return nil, nil, bytesRead, err
		}
	}
	return commonSchema, rows, bytesRead, nil
}
