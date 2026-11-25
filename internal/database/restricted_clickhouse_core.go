//go:build datafabric_core

package database

import (
	"database/sql"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// The Core build does not contain the ClickHouse native driver.
func openRestrictedClickHouse(_ model.DataSourceConfig, _ []byte) (*sql.DB, error) {
	return nil, ErrEgressDenied
}
