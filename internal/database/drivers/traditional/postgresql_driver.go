package traditional

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	_ "github.com/lib/pq"
)

// PostgreSQLDriver implements Driver for PostgreSQL
type PostgreSQLDriver struct{}

func (d *PostgreSQLDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	sql = strings.TrimSpace(sql)

	// Check if query already has LIMIT or OFFSET
	sqlUpper := strings.ToUpper(sql)
	if strings.Contains(sqlUpper, " LIMIT ") || strings.Contains(sqlUpper, " OFFSET ") {
		// For complex queries with existing pagination, add a subquery wrapper
		return fmt.Sprintf("SELECT * FROM (%s) AS batch_query LIMIT %d OFFSET %d", sql, batchSize, offset), nil

	}
	// Use standard LIMIT/OFFSET for most databases
	if offset > 0 {
		return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	return fmt.Sprintf("%s LIMIT %d", sql, batchSize), nil
}
func (d *PostgreSQLDriver) Open(dsn string) (*sql.DB, error) {
	return sql.Open("postgres", dsn)
}

func (d *PostgreSQLDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

func (d *PostgreSQLDriver) GetDefaultPort() int {
	return 5432
}

func (d *PostgreSQLDriver) BuildDSN(config *model.DataSourceConfig) string {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		config.Username,
		config.Password,
		config.Host,
		config.Port,
		config.Database,
	)

	// Add parameters
	params := []string{}
	if config.SSL {
		params = append(params, "sslmode=require")
	} else {
		params = append(params, "sslmode=disable")
	}
	if config.Timezone != "" {
		params = append(params, "TimeZone="+config.Timezone)
	}

	if len(params) > 0 {
		dsn += "?"
		for i, param := range params {
			if i > 0 {
				dsn += "&"
			}
			dsn += param
		}
	}

	return dsn
}

func (d *PostgreSQLDriver) GetDatabaseTypeName() string {
	return "postgresql"
}

func (d *PostgreSQLDriver) TestConnection(db *sql.DB) error {
	return db.Ping()
}

func (d *PostgreSQLDriver) GetDriverName() string {
	return "postgres"
}

func (d *PostgreSQLDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

func (d *PostgreSQLDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     true,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

func (d *PostgreSQLDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}
