package file_system

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHDFSORCDriverContract(t *testing.T) {
	host := strings.TrimSpace(os.Getenv("DATA_FABRIC_HDFS_CONTRACT_HOST"))
	if host == "" {
		t.Skip("set DATA_FABRIC_HDFS_CONTRACT_HOST to run the live HDFS ORC contract")
	}
	port := 8020
	if value := strings.TrimSpace(os.Getenv("DATA_FABRIC_HDFS_CONTRACT_PORT")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			t.Fatalf("invalid HDFS contract port %q", value)
		}
		port = parsed
	}
	username := strings.TrimSpace(os.Getenv("DATA_FABRIC_HDFS_CONTRACT_USERNAME"))
	if username == "" {
		username = "hadoop"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	config := hdfsCSVTestConfig(nil)
	config.Host = host
	config.Port = port
	config.Username = username
	config.Database = "/warehouse/events.orc"
	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil {
		t.Fatalf("HDFSCSVClientConfig() error = %v", err)
	}
	client, err := NewHDFSClient(ctx, hdfsConfig)
	if err != nil {
		t.Fatalf("NewHDFSClient() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close HDFS ORC fixture client: %v", err)
		}
	})
	if _, err := client.GetCapacity(ctx); err != nil {
		t.Fatalf("HDFS capacity probe failed: %v", err)
	}
	fixturePath := strings.TrimSpace(os.Getenv("DATA_FABRIC_HDFS_ORC_CONTRACT_FIXTURE"))
	if fixturePath == "" {
		fixturePath = filepath.Join("..", "object_storage", "testdata", "orc_split_elim.orc")
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read ORC fixture: %v", err)
	}
	if err := client.WriteFile(ctx, config.Database, data); err != nil {
		t.Fatalf("write ORC fixture to HDFS: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.DeleteFile(cleanupCtx, config.Database, false); err != nil {
			t.Errorf("delete HDFS ORC contract fixture: %v", err)
		}
	})
	info, err := client.GetFileInfo(ctx, config.Database)
	if err != nil {
		t.Fatalf("read HDFS ORC file metadata: %v", err)
	}
	if info == nil || info.IsDir || info.Size != int64(len(data)) {
		t.Fatalf("HDFS ORC metadata = %+v, want a %d-byte file", info, len(data))
	}
	db, err := (&HDFSORCDriver{}).OpenWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT userid, string1 FROM events WHERE userid = 2 ORDER BY string1 LIMIT 1")
	if err != nil {
		t.Fatalf("filtered ORC query error: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("filtered query returned no row: %v", rows.Err())
	}
	var id int64
	var value string
	if err := rows.Scan(&id, &value); err != nil {
		t.Fatalf("scan filtered ORC row: %v", err)
	}
	if id != 2 || value != "foo" {
		t.Fatalf("filtered query returned (%d, %q), want (2, foo)", id, value)
	}
	if rows.Next() {
		t.Fatal("filtered query returned more than one row")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("filtered query rows error = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close filtered query rows: %v", err)
	}
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) query error: %v", err)
	}
	if count != 25000 {
		t.Fatalf("COUNT(*) = %d, want 25000", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM events"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Fatalf("write error = %v, want read-only rejection", err)
	}
	t.Log("HDFS ORC contract passed against the pinned Apache Hadoop 3.5.0 cluster")
}
