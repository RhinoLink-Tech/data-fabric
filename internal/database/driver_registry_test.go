package database

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Every driver the registry hands out must be usable: it has to report its own
// database type name and a category. Drivers that embed a *drivers.DriverBase
// keep those values in the base, so registering the zero value instead of the
// constructor leaves the base nil and the accessors dereference it.
func TestRegisteredDriversReportTypeAndCategory(t *testing.T) {
	registry := NewDriverRegistry()

	types := registry.ListDrivers()
	if len(types) == 0 {
		t.Fatal("registry returned no drivers")
	}

	for _, dbType := range types {
		t.Run(string(dbType), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("driver for %s panicked: %v", dbType, r)
				}
			}()

			driver, err := registry.GetDriver(dbType)
			if err != nil {
				t.Fatalf("GetDriver(%s) returned error: %v", dbType, err)
			}
			if driver == nil {
				t.Fatalf("GetDriver(%s) returned a nil driver", dbType)
			}

			if got := driver.GetDatabaseTypeName(); got == "" {
				t.Errorf("GetDatabaseTypeName() = %q, want a non-empty type name", got)
			}
			if got := driver.GetCategory(); got == "" {
				t.Errorf("GetCategory() = %q, want a non-empty category", got)
			}
		})
	}
}

func TestOzoneDriversAreNotAdvertisedUntilClientOperationsExist(t *testing.T) {
	registry := NewDriverRegistry()
	ozoneTypes := []model.DatabaseType{
		model.DatabaseTypeOzoneParquet,
		model.DatabaseTypeOzoneText,
		model.DatabaseTypeOzoneAvro,
	}
	for _, dbType := range ozoneTypes {
		if registry.IsSupported(dbType) {
			t.Errorf("registry still advertises %s", dbType)
		}
		if _, err := registry.GetDriver(dbType); err == nil {
			t.Errorf("GetDriver(%s) error = nil, want unsupported type", dbType)
		}
	}
}
