//go:build datafabric_core

package database

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/traditional"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/warehouses"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// The experimental profile retains the reference relational sources and the
// Trino federation entry. Other types are not registered in this binary.
func (dr *DriverRegistry) registerDrivers() {
	dr.mutex.Lock()
	defer dr.mutex.Unlock()
	dr.register(model.DatabaseTypeMySQL, func() drivers.Driver { return &traditional.MySQLDriver{} })
	dr.register(model.DatabaseTypeMariaDB, func() drivers.Driver { return &traditional.MySQLDriver{} })
	dr.register(model.DatabaseTypePostgreSQL, func() drivers.Driver { return &traditional.PostgreSQLDriver{} })
	dr.register(model.DatabaseTypeTrino, func() drivers.Driver { return warehouses.NewTrinoDriver() })
}
