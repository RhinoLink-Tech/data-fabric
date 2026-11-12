package service

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// TestMySQLDistinctCountContract uses only a session-local temporary table.
// CI's full profile supplies an isolated MySQL service; local unit runs skip it.
func TestMySQLDistinctCountContract(t *testing.T) {
	host := os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_HOST")
	if host == "" {
		t.Skip("set the MySQL contract environment to run the live count test")
	}
	port := os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_PORT")
	user := os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_USERNAME")
	password := os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_PASSWORD")
	database := os.Getenv("DATA_FABRIC_MYSQL_CONTRACT_DATABASE")
	if port == "" || user == "" || password == "" || database == "" {
		t.Fatal("incomplete MySQL contract configuration")
	}

	config := mysql.NewConfig()
	config.User, config.Passwd = user, password
	config.Net, config.Addr = "tcp", net.JoinHostPort(host, port)
	config.DBName = database
	config.Timeout = 10 * time.Second
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal("open synthetic MySQL contract connection failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal("synthetic MySQL contract connection failed")
	}
	if _, err := db.ExecContext(ctx, "CREATE TEMPORARY TABLE issue_492_count (status VARCHAR(8) NOT NULL)"); err != nil {
		t.Fatalf("create temporary fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO issue_492_count(status) VALUES ('A'), ('A'), ('B')"); err != nil {
		t.Fatalf("seed temporary fixture: %v", err)
	}

	distinct := "SELECT DISTINCT status AS label, status AS label FROM issue_492_count"
	rows, err := db.QueryContext(ctx, distinct)
	if err != nil {
		t.Fatalf("query fixture: %v", err)
	}
	rowCount := 0
	for rows.Next() {
		rowCount++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read fixture: %v", err)
	}
	rows.Close()
	if rowCount != 2 {
		t.Fatalf("fixture returned %d distinct rows, want 2", rowCount)
	}

	count, err := countRowsInQuery(ctx, db, distinct)
	if err != nil && !errors.Is(err, ErrExactCountUnavailable) {
		t.Fatalf("unexpected exact-count error: %v", err)
	}
	if err == nil && count != 2 {
		t.Fatalf("distinct result has 2 rows but count returned %d", count)
	}

	ordinary := "SELECT status AS label, status AS label FROM issue_492_count"
	count, err = countRowsInQuery(ctx, db, ordinary)
	if err != nil || count != 3 {
		t.Fatalf("ordinary duplicate-column query: count=%d, err=%v; want 3", count, err)
	}
}
