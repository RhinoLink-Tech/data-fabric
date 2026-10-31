package file_system

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/colinmarc/hdfs/v2"
)

// HDFSClient handles HDFS operations
type HDFSClient struct {
	client *hdfs.Client
	config *HDFSConfig
}

// HDFSConfig holds HDFS configuration
type HDFSConfig struct {
	NameNodes      []string // List of NameNode addresses
	Username       string   // HDFS user
	KrbServiceName string   // Kerberos service name
	KrbRealm       string   // Kerberos realm
	KrbKeytab      string   // Kerberos keytab path
	BlockSize      int64    // Block size
	Replication    int      // Replication factor
}

// NewHDFSClient creates a new HDFS client
func NewHDFSClient(ctx context.Context, config *HDFSConfig) (*HDFSClient, error) {
	if len(config.NameNodes) == 0 {
		return nil, fmt.Errorf("at least one NameNode is required")
	}

	// Create HDFS client options
	opts := hdfs.ClientOptions{
		Addresses: config.NameNodes,
		User:      config.Username,
	}

	client, err := hdfs.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create HDFS client: %w", err)
	}

	return &HDFSClient{
		client: client,
		config: config,
	}, nil
}

// Close closes the HDFS client
func (c *HDFSClient) Close() error {
	return c.client.Close()
}

// ReadFileLimited reads at most maxBytes plus one sentinel byte from HDFS.
func (c *HDFSClient) ReadFileLimited(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("maximum HDFS file size must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, err := c.client.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open HDFS file: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read HDFS file: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("HDFS file exceeds configured maximum of %d bytes", maxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// ReadFile reads a file from HDFS
func (c *HDFSClient) ReadFile(ctx context.Context, path string) ([]byte, error) {
	reader, err := c.client.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open HDFS file: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read HDFS file: %w", err)
	}

	return data, nil
}

// WriteFile writes a file to HDFS
func (c *HDFSClient) WriteFile(ctx context.Context, path string, data []byte) error {
	// Use Create which returns an io.WriteCloser in this hdfs client
	writer, err := c.client.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create HDFS file: %w", err)
	}
	defer writer.Close()

	_, err = writer.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write HDFS file: %w", err)
	}

	return nil
}

// ListFiles lists files in a directory
func (c *HDFSClient) ListFiles(ctx context.Context, path string) ([]HDFSFileInfo, error) {
	fileInfos, err := c.client.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to list HDFS files: %w", err)
	}

	files := make([]HDFSFileInfo, len(fileInfos))
	for i, info := range fileInfos {
		// Use safe defaults; underlying hdfs.FileInfo may not expose Owner/BlockSize
		files[i] = HDFSFileInfo{
			Name:        info.Name(),
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			IsDir:       info.IsDir(),
			Owner:       "",
			Replication: 0,
		}
	}

	return files, nil
}

// DeleteFile deletes a file from HDFS
func (c *HDFSClient) DeleteFile(ctx context.Context, path string, recursive bool) error {
	err := c.client.Remove(path)
	if err != nil {
		return fmt.Errorf("failed to delete HDFS file: %w", err)
	}
	return nil
}

// GetFileInfo retrieves file information
func (c *HDFSClient) GetFileInfo(ctx context.Context, path string) (*HDFSFileInfo, error) {
	info, err := c.client.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Safe defaults for Owner and Replication
	return &HDFSFileInfo{
		Name:        info.Name(),
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		IsDir:       info.IsDir(),
		Owner:       "",
		Replication: 0,
	}, nil
}

// CreateDirectory creates a directory
func (c *HDFSClient) CreateDirectory(ctx context.Context, path string, recursive bool) error {
	if recursive {
		err := c.client.MkdirAll(path, 0755)
		if err != nil {
			return fmt.Errorf("failed to create directory recursively: %w", err)
		}
		return nil
	}
	err := c.client.Mkdir(path, 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// Exists checks if a path exists
func (c *HDFSClient) Exists(ctx context.Context, path string) (bool, error) {
	_, err := c.client.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Rename renames/moves a file
func (c *HDFSClient) Rename(ctx context.Context, oldPath, newPath string) error {
	err := c.client.Rename(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}
	return nil
}

// GetBlockLocations retrieves block locations for a file
func (c *HDFSClient) GetBlockLocations(ctx context.Context, path string) ([]HDFSBlockLocation, error) {
	fileInfo, err := c.client.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Return placeholder block locations
	// In real implementation, you'd fetch actual block locations
	blocks := []HDFSBlockLocation{
		{
			Offset: 0,
			Length: fileInfo.Size(),
			Hosts:  []string{"datanode1", "datanode2", "datanode3"},
		},
	}

	return blocks, nil
}

// HDFSFileInfo represents HDFS file information
type HDFSFileInfo struct {
	Name        string
	Size        int64
	ModTime     time.Time
	IsDir       bool
	Owner       string
	Replication int
}

// HDFSBlockLocation represents HDFS block location
type HDFSBlockLocation struct {
	Offset int64
	Length int64
	Hosts  []string
}

// ReadFileRange reads a specific range of a file
func (c *HDFSClient) ReadFileRange(ctx context.Context, path string, offset, length int64) ([]byte, error) {
	reader, err := c.client.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open HDFS file: %w", err)
	}
	defer reader.Close()

	_, err = reader.Seek(offset, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek in file: %w", err)
	}

	data := make([]byte, length)
	_, err = io.ReadFull(reader, data)
	if err != nil {
		return nil, fmt.Errorf("failed to read file range: %w", err)
	}

	return data, nil
}

// SetReplication sets replication factor for a file
func (c *HDFSClient) SetReplication(ctx context.Context, path string, replication int) error {
	// Not supported by current hdfs client version; no-op for compatibility
	return nil
}

// GetCapacity retrieves HDFS cluster capacity information
func (c *HDFSClient) GetCapacity(ctx context.Context) (*HDFSCapacity, error) {
	// Capacity API not available on this client version; return zeros
	return &HDFSCapacity{
		Total:     0,
		Used:      0,
		Remaining: 0,
	}, nil
}

// HDFSCapacity represents HDFS capacity information
type HDFSCapacity struct {
	Total     uint64
	Used      uint64
	Remaining uint64
}
