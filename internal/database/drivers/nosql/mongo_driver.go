package nosql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MongoDriver exposes a restricted SQL query surface that the gateway maps to
// native MongoDB find/count commands. MongoDB itself does not speak SQL.
type MongoDriver struct {
	base *drivers.DriverBase
}

func NewMongoDriver() *MongoDriver {
	return &MongoDriver{base: drivers.NewDriverBase(model.DatabaseTypeMongoDB, drivers.CategoryNoSQL)}
}

func (md *MongoDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return "", fmt.Errorf("MongoDB query is required")
	}
	if batchSize <= 0 {
		return "", fmt.Errorf("batch size must be positive")
	}
	if offset < 0 {
		return "", fmt.Errorf("offset cannot be negative")
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, offset), nil
}

func (md *MongoDriver) Open(dsn string) (*sql.DB, error) {
	database, err := mongoDatabaseFromURI(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(&mongoSQLConnector{uri: dsn, database: database}), nil
}

func (md *MongoDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("MongoDB data source config is required")
	}
	if strings.TrimSpace(config.Host) == "" {
		return nil, fmt.Errorf("MongoDB host is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return nil, fmt.Errorf("MongoDB database is required")
	}
	dsn := md.BuildDSN(config)
	if err := md.ValidateDSN(dsn); err != nil {
		return nil, err
	}
	return sql.OpenDB(&mongoSQLConnector{uri: dsn, database: config.Database}), nil
}

func mongoDatabaseFromURI(dsn string) (string, error) {
	parsed, err := connstring.ParseAndValidate(dsn)
	if err != nil {
		return "", fmt.Errorf("invalid MongoDB DSN format: %w", err)
	}
	if strings.TrimSpace(parsed.Database) == "" {
		return "", fmt.Errorf("MongoDB database name is required in the connection URI")
	}
	return parsed.Database, nil
}

func (md *MongoDriver) ValidateDSN(dsn string) error {
	_, err := mongoDatabaseFromURI(dsn)
	return err
}

func (md *MongoDriver) GetDefaultPort() int { return 27017 }

func (md *MongoDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	port := config.Port
	if port <= 0 {
		port = md.GetDefaultPort()
	}
	parsed := &url.URL{
		Scheme: "mongodb",
		Host:   net.JoinHostPort(config.Host, strconv.Itoa(port)),
		Path:   "/" + config.Database,
	}
	if config.Username != "" {
		parsed.User = url.UserPassword(config.Username, config.Password)
	}
	query := parsed.Query()
	for key, value := range config.AdditionalProps {
		query.Set(key, fmt.Sprint(value))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (md *MongoDriver) GetDatabaseTypeName() string { return md.base.GetDatabaseTypeName() }

func (md *MongoDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("MongoDB connection is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

func (md *MongoDriver) GetDriverName() string               { return "mongo-go-driver" }
func (md *MongoDriver) GetCategory() drivers.DriverCategory { return md.base.GetCategory() }

func (md *MongoDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: false,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

func (md *MongoDriver) ConfigureAuth(interface{}) error { return nil }

// mongoSQLConnector shares one thread-safe MongoDB client across database/sql
// connections so the Mongo driver's native socket pool can serve concurrent
// queries without creating a separate client pool for each SQL connection.
type mongoSQLConnector struct {
	uri      string
	database string

	mu     sync.Mutex
	client *mongo.Client
	refs   int
}

func (c *mongoSQLConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		client, err := mongo.Connect(ctx, options.Client().ApplyURI(c.uri))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
		}
		c.client = client
	}
	c.refs++
	return &mongoSQLConn{
		client: c.client, database: c.client.Database(c.database), connector: c,
	}, nil
}

func (c *mongoSQLConnector) release(ctx context.Context, client *mongo.Client) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != client || c.refs == 0 {
		return nil
	}
	c.refs--
	if c.refs > 0 {
		return nil
	}
	c.client = nil
	return client.Disconnect(ctx)
}

func (c *mongoSQLConnector) Driver() driver.Driver { return mongoSQLDriver{connector: c} }

type mongoSQLDriver struct{ connector *mongoSQLConnector }

func (d mongoSQLDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}
