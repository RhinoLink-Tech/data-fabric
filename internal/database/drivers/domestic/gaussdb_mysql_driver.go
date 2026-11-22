package domestic

import (
	"database/sql"
	"fmt"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/traditional"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// GaussDBMySQLDriver uses the MySQL wire adapter. Compatibility with a
// particular GaussDB release must be established separately.
type GaussDBMySQLDriver struct{ traditional.MySQLDriver }

func (d *GaussDBMySQLDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	// Preserve the existing GaussDB TLS fail-closed contract until verified
	// against a pinned GaussDB MySQL-compatible endpoint.
	if unsupportedDomesticTLS(config, "") {
		return unsupportedDomesticTLSDSN
	}
	return d.MySQLDriver.BuildDSN(config)
}

func (d *GaussDBMySQLDriver) ValidateDSN(dsn string) error {
	if dsn == unsupportedDomesticTLSDSN {
		return fmt.Errorf("requested TLS configuration is unsupported by this driver")
	}
	return d.MySQLDriver.ValidateDSN(dsn)
}

func (d *GaussDBMySQLDriver) Open(dsn string) (*sql.DB, error) {
	if err := d.ValidateDSN(dsn); err != nil {
		return nil, err
	}
	return d.MySQLDriver.Open(dsn)
}

func (d *GaussDBMySQLDriver) OpenForHealthCheck(dsn string) (*sql.DB, error) { return d.Open(dsn) }
func (d *GaussDBMySQLDriver) GetDatabaseTypeName() string                    { return "gaussdb_mysql" }
func (d *GaussDBMySQLDriver) GetDriverName() string                          { return "gaussdb-mysql" }
func (d *GaussDBMySQLDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsTransaction: true, SupportsSchemaDiscovery: true}
}
