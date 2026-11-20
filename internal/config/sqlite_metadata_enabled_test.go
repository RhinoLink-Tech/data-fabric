//go:build datafabric_sqlite && cgo

package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func openSQLiteFixture(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := InitDatabase(&Config{Database: DatabaseConfig{Driver: "sqlite", SQLitePath: path}})
	if err != nil {
		t.Fatalf("open SQLite fixture: %v", err)
	}
	t.Cleanup(func() {
		raw, _ := db.DB()
		if raw != nil {
			_ = raw.Close()
		}
	})
	return db
}

func TestSQLiteMetadataPersistsAndUsesSafePragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	db := openSQLiteFixture(t, path)
	if err := db.Exec("CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO counter VALUES (1, 7)").Error; err != nil {
		t.Fatal(err)
	}
	var journal string
	var sync, busy, foreign int
	for q, out := range map[string]any{"PRAGMA journal_mode": &journal, "PRAGMA synchronous": &sync, "PRAGMA busy_timeout": &busy, "PRAGMA foreign_keys": &foreign} {
		if err := db.Raw(q).Scan(out).Error; err != nil {
			t.Fatal(err)
		}
	}
	if !strings.EqualFold(journal, "wal") || sync != 2 || busy != 5000 || foreign != 1 {
		t.Fatalf("PRAGMAs journal=%s sync=%d busy=%d foreign=%d", journal, sync, busy, foreign)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("SQLite sidecar %s mode %v err=%v", suffix, info, err)
		}
	}
	raw, _ := db.DB()
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openSQLiteFixture(t, path)
	var value int
	if err := reopened.Raw("SELECT value FROM counter WHERE id = 1").Scan(&value).Error; err != nil || value != 7 {
		t.Fatalf("reopened value=%d err=%v", value, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode %v err=%v", info, err)
	}
}

func TestSQLiteMetadataRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "public.sqlite")
	if err := os.WriteFile(public, nil, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.sqlite")
	if err := os.Symlink(public, link); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(dir, "shared")
	if err := os.Mkdir(shared, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0777); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "relative.sqlite", dir, public, link, filepath.Join(dir, "missing", "file.sqlite"), filepath.Join(shared, "file.sqlite")} {
		t.Run(fmt.Sprintf("%q", path), func(t *testing.T) {
			_, err := InitDatabase(&Config{Database: DatabaseConfig{Driver: "sqlite", SQLitePath: path}})
			if err == nil {
				t.Fatalf("accepted unsafe path %q", path)
			}
		})
	}
}

func TestSQLiteMetadataCrossProcessTransactions(t *testing.T) {
	if os.Getenv("DATA_FABRIC_SQLITE_TEST_WORKER") == "1" {
		db := openSQLiteFixture(t, os.Getenv("DATA_FABRIC_SQLITE_TEST_PATH"))
		for i := 0; i < 20; i++ {
			err := db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
				var value int
				if e := tx.Raw("SELECT value FROM counter WHERE id = 1").Scan(&value).Error; e != nil {
					return e
				}
				return tx.Exec("UPDATE counter SET value = ? WHERE id = 1", value+1).Error
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	db := openSQLiteFixture(t, path)
	if err := db.Exec("CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO counter VALUES (1, 0)").Error; err != nil {
		t.Fatal(err)
	}
	cmds := make([]*exec.Cmd, 2)
	for i := range cmds {
		cmds[i] = exec.Command(os.Args[0], "-test.run=^TestSQLiteMetadataCrossProcessTransactions$", "-test.count=1")
		cmds[i].Env = append(os.Environ(), "DATA_FABRIC_SQLITE_TEST_WORKER=1", "DATA_FABRIC_SQLITE_TEST_PATH="+path)
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("worker failed: %v", err)
		}
	}
	var value int
	if err := db.Raw("SELECT value FROM counter WHERE id = 1").Scan(&value).Error; err != nil || value != 40 {
		t.Fatalf("cross-process value=%d err=%v", value, err)
	}
}
