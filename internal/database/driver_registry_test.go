package database

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestDriverRegistryGetDriverAndIsSupported(t *testing.T) {
	registry := NewDriverRegistry()
	tests := []struct {
		name      string
		dbType    model.DatabaseType
		supported bool
	}{
		{name: "mysql", dbType: model.DatabaseTypeMySQL, supported: true},
		{name: "postgresql", dbType: model.DatabaseTypePostgreSQL, supported: true},
		{name: "unknown", dbType: model.DatabaseType("unknown")},
		{name: "empty", dbType: model.DatabaseType("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := registry.IsSupported(tt.dbType); got != tt.supported {
				t.Errorf("IsSupported(%q) = %v, want %v", tt.dbType, got, tt.supported)
			}

			driver, err := registry.GetDriver(tt.dbType)
			if tt.supported {
				if err != nil {
					t.Fatalf("GetDriver(%q) returned error: %v", tt.dbType, err)
				}
				if driver == nil {
					t.Errorf("GetDriver(%q) returned a nil driver", tt.dbType)
				}
				return
			}
			if err == nil {
				t.Errorf("GetDriver(%q) error = nil, want unsupported type error", tt.dbType)
			}
			if driver != nil {
				t.Errorf("GetDriver(%q) returned a driver for an unsupported type", tt.dbType)
			}
		})
	}
}

func TestDriverRegistrySupportedTypeLists(t *testing.T) {
	registry := NewDriverRegistry()
	lists := []struct {
		name  string
		types []model.DatabaseType
	}{
		{name: "ListDrivers", types: registry.ListDrivers()},
		{name: "GetSupportedTypes", types: registry.GetSupportedTypes()},
	}
	sets := make([]map[model.DatabaseType]bool, len(lists))

	for i, list := range lists {
		sets[i] = make(map[model.DatabaseType]bool, len(list.types))
		t.Run(list.name, func(t *testing.T) {
			if len(list.types) == 0 {
				t.Error("returned no supported database types")
			}
			for _, dbType := range list.types {
				if sets[i][dbType] {
					t.Errorf("returned duplicate database type %q", dbType)
				}
				sets[i][dbType] = true
				if !registry.IsSupported(dbType) {
					t.Errorf("returned unsupported database type %q", dbType)
				}
			}
		})
	}

	if len(sets[0]) != len(sets[1]) {
		t.Errorf("ListDrivers returned %d unique types, GetSupportedTypes returned %d", len(sets[0]), len(sets[1]))
	}
	for dbType := range sets[0] {
		if !sets[1][dbType] {
			t.Errorf("GetSupportedTypes is missing type %q returned by ListDrivers", dbType)
		}
	}
}

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
