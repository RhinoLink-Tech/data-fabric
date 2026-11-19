package database

import (
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"

	"sync"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// DriverRegistry manages driver instances and creation
type DriverRegistry struct {
	drivers map[model.DatabaseType]func() drivers.Driver
	mutex   sync.RWMutex
}

// NewDriverRegistry creates a new driver registry
func NewDriverRegistry() *DriverRegistry {
	registry := &DriverRegistry{
		drivers: make(map[model.DatabaseType]func() drivers.Driver),
	}

	// Register the drivers selected at build time.
	registry.registerDrivers()

	return registry
}

// register registers a driver factory function
func (dr *DriverRegistry) register(dbType model.DatabaseType, factory func() drivers.Driver) {
	dr.drivers[dbType] = factory
}

// RegisterDriver registers a concrete driver instance for a database type.
// This mirrors older code that allowed drivers to self-register and keeps
// backward compatibility with driver packages that call this method.
func (dr *DriverRegistry) RegisterDriver(dbType model.DatabaseType, driver drivers.Driver) {
	dr.mutex.Lock()
	defer dr.mutex.Unlock()
	dr.drivers[dbType] = func() drivers.Driver { return driver }
}

// GetDriver creates or retrieves a driver for the specified database type
func (dr *DriverRegistry) GetDriver(dbType model.DatabaseType) (drivers.Driver, error) {
	dr.mutex.RLock()
	factory, exists := dr.drivers[dbType]
	dr.mutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	return factory(), nil
}

// ListDrivers returns all supported database types
func (dr *DriverRegistry) ListDrivers() []model.DatabaseType {
	dr.mutex.RLock()
	defer dr.mutex.RUnlock()

	types := make([]model.DatabaseType, 0, len(dr.drivers))
	for dbType := range dr.drivers {
		types = append(types, dbType)
	}

	return types
}

// IsSupported checks if a database type is supported
func (dr *DriverRegistry) IsSupported(dbType model.DatabaseType) bool {
	dr.mutex.RLock()
	_, exists := dr.drivers[dbType]
	dr.mutex.RUnlock()

	return exists
}

// GetDriverCategory returns the category for a database type
func (dr *DriverRegistry) GetDriverCategory(dbType model.DatabaseType) (drivers.DriverCategory, error) {
	driver, err := dr.GetDriver(dbType)
	if err != nil {
		return "", err
	}

	return driver.GetCategory(), nil
}

// GetSupportedTypes returns all supported database types
func (dr *DriverRegistry) GetSupportedTypes() []model.DatabaseType {
	return dr.ListDrivers()
}
