package nosql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	mongoContractHostEnv            = "DATA_FABRIC_MONGODB_CONTRACT_HOST"
	mongoContractPortEnv            = "DATA_FABRIC_MONGODB_CONTRACT_PORT"
	mongoContractUsernameEnv        = "DATA_FABRIC_MONGODB_CONTRACT_USERNAME"
	mongoContractPasswordEnv        = "DATA_FABRIC_MONGODB_CONTRACT_PASSWORD"
	mongoContractDatabaseEnv        = "DATA_FABRIC_MONGODB_CONTRACT_DATABASE"
	mongoContractBootstrapUserEnv   = "DATA_FABRIC_MONGODB_BOOTSTRAP_USERNAME"
	mongoContractBootstrapPassEnv   = "DATA_FABRIC_MONGODB_BOOTSTRAP_PASSWORD"
	mongoContractExpectedVersionEnv = "DATA_FABRIC_MONGODB_EXPECTED_VERSION"
)

func TestMongoDriverContract(t *testing.T) {
	host := os.Getenv(mongoContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the MongoDB server contract", mongoContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(mongoContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", mongoContractPortEnv)
	}
	contractUsername := os.Getenv(mongoContractUsernameEnv)
	contractPassword := os.Getenv(mongoContractPasswordEnv)
	databaseName := os.Getenv(mongoContractDatabaseEnv)
	bootstrapUsername := os.Getenv(mongoContractBootstrapUserEnv)
	bootstrapPassword := os.Getenv(mongoContractBootstrapPassEnv)
	expectedVersion := os.Getenv(mongoContractExpectedVersionEnv)
	if contractUsername == "" || contractPassword == "" || databaseName == "" || bootstrapUsername == "" || bootstrapPassword == "" || expectedVersion == "" {
		t.Fatal("MongoDB contract credentials, database, and expected server version are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))
	adminClient, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoContractAdminURI(host, port, bootstrapUsername, bootstrapPassword)))
	if err != nil {
		t.Fatalf("connect bootstrap account to MongoDB: %v", err)
	}
	defer adminClient.Disconnect(context.Background())
	if err := adminClient.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatalf("authenticate bootstrap account at %s: %v", endpoint, err)
	}

	var buildInfo struct {
		Version string `bson:"version"`
	}
	if err := adminClient.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&buildInfo); err != nil {
		t.Fatalf("read MongoDB build information: %v", err)
	}
	if buildInfo.Version != expectedVersion {
		t.Fatalf("MongoDB server version = %q, want exact contract version %q", buildInfo.Version, expectedVersion)
	}

	contractUser := fmt.Sprintf("%s_%d", contractUsername, time.Now().UnixNano())
	authDatabase := adminClient.Database(databaseName)
	createUser := bson.D{
		{Key: "createUser", Value: contractUser},
		{Key: "pwd", Value: contractPassword},
		{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "readWrite"}, {Key: "db", Value: databaseName}}}},
	}
	if err := authDatabase.RunCommand(ctx, createUser).Err(); err != nil {
		t.Fatalf("create least-privilege MongoDB contract account: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = authDatabase.RunCommand(cleanupCtx, bson.D{{Key: "dropUser", Value: contractUser}}).Err()
	}()

	driver := NewMongoDriver()
	config := &model.DataSourceConfig{
		Host:     host,
		Port:     port,
		Database: databaseName,
		Username: contractUser,
		Password: contractPassword,
		AdditionalProps: map[string]interface{}{
			"serverSelectionTimeoutMS": 5000,
			"connectTimeoutMS":         5000,
		},
	}
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN: %v", err)
	}
	if driver.GetDefaultPort() != 27017 {
		t.Fatalf("default port = %d, want 27017", driver.GetDefaultPort())
	}

	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	if err := driver.TestConnection(db); err != nil {
		t.Fatalf("TestConnection with database-scoped username/password: %v", err)
	}

	collectionName := fmt.Sprintf("data_fabric_contract_rows_%d", time.Now().UnixNano())
	appConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire MongoDB adapter connection: %v", err)
	}
	defer appConn.Close()
	var collection *mongo.Collection
	if err := appConn.Raw(func(driverConn interface{}) error {
		mongoConn, ok := driverConn.(*mongoSQLConn)
		if !ok {
			return fmt.Errorf("unexpected MongoDB adapter connection type %T", driverConn)
		}
		collection = mongoConn.client.Database(databaseName).Collection(collectionName)
		return nil
	}); err != nil {
		t.Fatalf("get collection through the authenticated adapter client: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = collection.Drop(cleanupCtx)
	}()
	_, err = collection.InsertMany(ctx, []interface{}{
		bson.D{{Key: "name", Value: "Ada"}, {Key: "age", Value: int32(37)}, {Key: "active", Value: true}, {Key: "nickname", Value: nil}},
		bson.D{{Key: "name", Value: "Bob"}, {Key: "age", Value: int32(20)}, {Key: "active", Value: true}, {Key: "nickname", Value: "bobby"}},
		bson.D{{Key: "name", Value: "Lin"}, {Key: "age", Value: int32(42)}, {Key: "active", Value: false}, {Key: "nickname", Value: "lin"}},
		bson.D{{Key: "name", Value: "Eve"}, {Key: "age", Value: int32(29)}, {Key: "active", Value: true}, {Key: "nickname", Value: "eve"}},
	})
	if err != nil {
		t.Fatalf("insert MongoDB contract documents: %v", err)
	}

	t.Run("SQL projection, filter, bind values, and ordering", func(t *testing.T) {
		rows, err := db.QueryContext(ctx,
			"SELECT name AS person FROM "+collectionName+" WHERE active = ? AND age >= ? ORDER BY age DESC LIMIT ? OFFSET ?",
			true, int64(18), int64(2), int64(0),
		)
		if err != nil {
			t.Fatalf("execute restricted SELECT: %v", err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatalf("read result columns: %v", err)
		}
		if fmt.Sprint(columns) != "[person]" {
			t.Fatalf("result columns = %v, want [person]", columns)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatalf("scan projected value: %v", err)
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read projected rows: %v", err)
		}
		if fmt.Sprint(names) != "[Ada Eve]" {
			t.Fatalf("query rows = %v, want [Ada Eve]", names)
		}
	})

	t.Run("batch pagination", func(t *testing.T) {
		query, err := driver.ApplyBatchPagination("SELECT name FROM "+collectionName+" WHERE active = true ORDER BY age DESC", 2, 1)
		if err != nil {
			t.Fatalf("ApplyBatchPagination: %v", err)
		}
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("execute batch page %q: %v", query, err)
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatalf("scan page value: %v", err)
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read page rows: %v", err)
		}
		if fmt.Sprint(names) != "[Eve Bob]" {
			t.Fatalf("page rows = %v, want [Eve Bob]", names)
		}
	})

	t.Run("native document count and null scanning", func(t *testing.T) {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) AS total FROM "+collectionName+" WHERE active = true").Scan(&count); err != nil {
			t.Fatalf("query native document count: %v", err)
		}
		if count != 3 {
			t.Fatalf("active document count = %d, want 3", count)
		}

		var nickname sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT nickname FROM "+collectionName+" WHERE name = ?", "Ada").Scan(&nickname); err != nil {
			t.Fatalf("scan nullable document field: %v", err)
		}
		if nickname.Valid {
			t.Fatalf("null nickname scanned as %+v, want invalid sql.NullString", nickname)
		}
	})

	t.Run("writes and unsupported SQL are rejected", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+collectionName); err == nil || !strings.Contains(strings.ToLower(err.Error()), "select") {
			t.Fatalf("DELETE error = %v, want adapter to reject non-SELECT statements", err)
		}
		if _, err := db.QueryContext(ctx, "SELECT * FROM "+collectionName+" WHERE active = true OR age > 20"); err == nil {
			t.Fatal("unsupported OR predicate unexpectedly succeeded")
		}
	})

	t.Run("query cancellation", func(t *testing.T) {
		queryCtx, stop := context.WithCancel(ctx)
		stop()
		_, err := db.QueryContext(queryCtx, "SELECT * FROM "+collectionName)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled query error = %v, want context.Canceled", err)
		}
	})
}

func mongoContractAdminURI(host string, port int, username, password string) string {
	parsed := &url.URL{
		Scheme: "mongodb",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/admin",
		User:   url.UserPassword(username, password),
	}
	query := parsed.Query()
	query.Set("authSource", "admin")
	query.Set("serverSelectionTimeoutMS", "5000")
	query.Set("connectTimeoutMS", "5000")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
