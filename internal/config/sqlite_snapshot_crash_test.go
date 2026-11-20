//go:build datafabric_sqlite && cgo

package config_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteMetadataInterruptedWriteRecovery(t *testing.T) {
	path := os.Getenv("DATA_FABRIC_SQLITE_CRASH_PATH")
	if path != "" {
		db := openMetadata(t, path)
		tx := db.Begin()
		if tx.Error != nil {
			t.Fatal(tx.Error)
		}
		if err := tx.Exec("UPDATE recovery_check SET value = 2 WHERE id = 1").Error; err != nil {
			t.Fatal(err)
		}
		fmt.Println("TRANSACTION_READY")
		time.Sleep(30 * time.Second)
		t.Fatal("parent failed to interrupt worker")
	}
	path = filepath.Join(t.TempDir(), "source.sqlite")
	db := openMetadata(t, path)
	if err := db.Exec("CREATE TABLE recovery_check (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO recovery_check VALUES (1, 1)").Error; err != nil {
		t.Fatal(err)
	}
	closeMetadata(t, db)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteMetadataInterruptedWriteRecovery$", "-test.count=1")
	cmd.Env = append(os.Environ(), "DATA_FABRIC_SQLITE_CRASH_PATH="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			if scanner.Text() == "TRANSACTION_READY" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			_ = cmd.Wait()
			t.Fatal("worker exited before uncommitted write")
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("worker did not reach transaction")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	reopened := openMetadata(t, path)
	defer closeMetadata(t, reopened)
	var value int
	if err = reopened.Raw("SELECT value FROM recovery_check WHERE id = 1").Scan(&value).Error; err != nil || value != 1 {
		t.Fatalf("uncommitted update survived interruption: value=%d err=%v", value, err)
	}
	var check string
	if err = reopened.Raw("PRAGMA integrity_check").Scan(&check).Error; err != nil || check != "ok" {
		t.Fatalf("integrity after interrupted write: %s %v", check, err)
	}
}
