package file_system

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHDFSCSVDriverContract(t *testing.T) {
	host := strings.TrimSpace(os.Getenv("DATA_FABRIC_HDFS_CONTRACT_HOST"))
	if host == "" {
		t.Skip("set DATA_FABRIC_HDFS_CONTRACT_HOST to run the live HDFS contract")
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
	config.Database = "/warehouse/events.csv"

	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil {
		t.Fatalf("HDFSCSVClientConfig() error = %v", err)
	}
	client, err := NewHDFSClient(ctx, hdfsConfig)
	if err != nil {
		t.Fatalf("NewHDFSClient() error = %v", err)
	}
	defer client.Close()

	if _, err := client.GetCapacity(ctx); err != nil {
		t.Fatalf("HDFS capacity probe failed: %v", err)
	}
	data := []byte("id,name,active\n1,Ada,true\n2,Lin,false\n3,Mei,true\n")
	if err := client.WriteFile(ctx, config.Database, data); err != nil {
		t.Fatalf("write CSV fixture to HDFS: %v", err)
	}
	info, err := client.GetFileInfo(ctx, config.Database)
	if err != nil {
		t.Fatalf("read HDFS file metadata: %v", err)
	}
	if info == nil || info.IsDir || info.Size != int64(len(data)) {
		t.Fatalf("HDFS CSV metadata = %+v, want a %d-byte file", info, len(data))
	}

	db, err := (&HDFSCSVDriver{}).OpenWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT id, name FROM events WHERE id >= 2 ORDER BY id DESC LIMIT 1")
	if err != nil {
		t.Fatalf("filtered CSV query error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("filtered query returned no row: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("scan filtered query row: %v", err)
	}
	if id != 3 || name != "Mei" {
		t.Fatalf("filtered query returned (%d, %q), want (3, Mei)", id, name)
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
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) query error = %v", err)
	}
	if count != 2 {
		t.Fatalf("COUNT(*) = %d, want 2", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM events"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Fatalf("write error = %v, want read-only rejection", err)
	}

}
