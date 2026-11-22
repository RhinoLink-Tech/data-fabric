package domestic

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	goora "github.com/sijms/go-ora/v2"
	"github.com/sijms/go-ora/v2/configurations"
)

// BuildDSN builds an Oracle URL from the supplied source settings. Schema is
// consumed by our connector, not forwarded as an unsupported go-ora option.
func (d *OceanBaseOracleDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil || config.Host == "" || config.Port < 1 || config.Port > 65535 {
		return ""
	}
	if unsupportedDomesticTLS(config, "") {
		return unsupportedDomesticTLSDSN
	}
	charset, schema := "AL32UTF8", config.Schema
	if d.config != nil {
		if d.config.Charset != "" {
			charset = d.config.Charset
		}
		if schema == "" {
			schema = d.config.Schema
		}
	}
	host := strings.TrimPrefix(strings.TrimSuffix(config.Host, "]"), "[")
	u := &url.URL{Scheme: "oracle", User: url.UserPassword(config.Username, config.Password), Host: net.JoinHostPort(host, strconv.Itoa(config.Port)), Path: "/" + config.Database, RawPath: "/" + url.PathEscape(config.Database)}
	q := url.Values{"charset": {charset}}
	if schema != "" {
		q.Set("schema", schema)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// oracleConnectionOptions removes the local schema option before go-ora sees
// it and validates the remaining URL using the actual driver's parser.
func oracleConnectionOptions(dsn string) (string, string, error) {
	if dsn == unsupportedDomesticTLSDSN {
		return "", "", fmt.Errorf("requested TLS configuration is unsupported by this driver")
	}
	u, err := url.Parse(dsn)
	if err != nil || u == nil || u.Scheme != "oracle" || u.User == nil || u.Hostname() == "" || u.Port() == "" || u.Path == "" {
		return "", "", fmt.Errorf("invalid OceanBase Oracle connection configuration")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", "", fmt.Errorf("invalid OceanBase Oracle connection configuration")
	}
	schema := ""
	for key, values := range q {
		if strings.EqualFold(key, "schema") {
			if schema != "" || len(values) != 1 || values[0] == "" {
				return "", "", fmt.Errorf("invalid OceanBase Oracle schema")
			}
			schema = values[0]
			q.Del(key)
		}
	}
	if !utf8.ValidString(schema) || strings.IndexFunc(schema, unicode.IsControl) >= 0 {
		return "", "", fmt.Errorf("invalid OceanBase Oracle schema")
	}
	u.RawQuery = q.Encode()
	sanitized := u.String()
	if _, err := configurations.ParseConfig(sanitized); err != nil {
		return "", "", fmt.Errorf("invalid OceanBase Oracle connection configuration")
	}
	return sanitized, schema, nil
}
func (d *OceanBaseOracleDriver) ValidateDSN(dsn string) error {
	_, _, err := oracleConnectionOptions(dsn)
	return err
}

func (d *OceanBaseOracleDriver) OpenForHealthCheck(dsn string) (*sql.DB, error) {
	clean, schema, err := oracleConnectionOptions(dsn)
	if err != nil {
		return nil, err
	}
	var connector driver.Connector = goora.NewConnector(clean)
	if schema != "" {
		connector = oracleSchemaConnector{base: connector, schema: schema}
	}
	return sql.OpenDB(connector), nil
}
func (d *OceanBaseOracleDriver) Open(dsn string) (*sql.DB, error) {
	db, err := d.OpenForHealthCheck(dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping OceanBase Oracle: %w", err)
	}
	return db, nil
}

// Apply schema to every physical connection, including after pool recycling.
type oracleSchemaConnector struct {
	base   driver.Connector
	schema string
}

func (c oracleSchemaConnector) Driver() driver.Driver { return c.base.Driver() }
func (c oracleSchemaConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("OceanBase Oracle connection cannot set schema")
	}
	quoted := `"` + strings.ReplaceAll(c.schema, `"`, `""`) + `"`
	if _, err := execer.ExecContext(ctx, "ALTER SESSION SET CURRENT_SCHEMA = "+quoted, nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set OceanBase Oracle session schema: %w", err)
	}
	return conn, nil
}
