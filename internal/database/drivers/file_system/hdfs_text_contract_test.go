package file_system

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHDFSTextDriverContract(t *testing.T) {
	host := strings.TrimSpace(os.Getenv("DATA_FABRIC_HDFS_CONTRACT_HOST"))
	if host == "" {
		t.Skip("set DATA_FABRIC_HDFS_CONTRACT_HOST to run the live HDFS text contract")
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
	config.Database = "/warehouse/events.txt"

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
			t.Errorf("close HDFS text fixture client: %v", err)
		}
	})

	if _, err := client.GetCapacity(ctx); err != nil {
		t.Fatalf("HDFS capacity probe failed: %v", err)
	}
	data := []byte("first\nsecond\r\n\r\nlast")
	if err := client.WriteFile(ctx, config.Database, data); err != nil {
		t.Fatalf("write text fixture to HDFS: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.DeleteFile(cleanupCtx, config.Database, false); err != nil {
			t.Errorf("delete HDFS text contract fixture: %v", err)
		}
	})
	info, err := client.GetFileInfo(ctx, config.Database)
	if err != nil {
		t.Fatalf("read HDFS text file metadata: %v", err)
	}
	if info == nil || info.IsDir || info.Size != int64(len(data)) {
		t.Fatalf("HDFS text metadata = %+v, want a %d-byte file", info, len(data))
	}

	db, err := (&HDFSTextDriver{}).OpenWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT line_number, line FROM events WHERE line_number >= 2 ORDER BY line_number DESC LIMIT 3")
	if err != nil {
		t.Fatalf("ordered text query error = %v", err)
	}
	want := []struct {
		lineNumber int64
		line       string
	}{
		{lineNumber: 4, line: "last"},
		{lineNumber: 3, line: ""},
		{lineNumber: 2, line: "second"},
	}
	for _, expected := range want {
		if !rows.Next() {
			t.Fatalf("text query returned fewer than %d rows: %v", len(want), rows.Err())
		}
		var lineNumber int64
		var line string
		if err := rows.Scan(&lineNumber, &line); err != nil {
			t.Fatalf("scan HDFS text row: %v", err)
		}
		if lineNumber != expected.lineNumber || line != expected.line {
			t.Fatalf("text row = (%d, %q), want (%d, %q)", lineNumber, line, expected.lineNumber, expected.line)
		}
	}
	if rows.Next() {
		t.Fatal("text query returned more rows than requested")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("text query rows error = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close text query rows: %v", err)
	}

	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) query error = %v", err)
	}
	if count != 4 {
		t.Fatalf("COUNT(*) = %d, want 4", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM events"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Fatalf("write error = %v, want read-only rejection", err)
	}
	t.Log("HDFS Text line-query contract passed against the pinned Apache Hadoop 3.5.0 cluster")
}
