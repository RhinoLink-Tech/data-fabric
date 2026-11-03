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

func TestOzoneParquetTextAndAvroUseS3GatewayAdapters(t *testing.T) {
	registry := NewDriverRegistry()
	driver, err := registry.GetDriver(model.DatabaseTypeOzoneParquet)
	if err != nil {
		t.Fatalf("GetDriver(ozone_parquet) error = %v", err)
	}
	if driver.GetCategory() != "object_storage" || !driver.GetCapabilities().SupportsSQL {
		t.Fatalf("Ozone Parquet driver category/capabilities = %q/%+v", driver.GetCategory(), driver.GetCapabilities())
	}
	textDriver, err := registry.GetDriver(model.DatabaseTypeOzoneText)
	if err != nil || !textDriver.GetCapabilities().SupportsSQL {
		t.Fatalf("Ozone text driver = %v, error = %v; want registered SQL adapter", textDriver, err)
	}
	avroDriver, err := registry.GetDriver(model.DatabaseTypeOzoneAvro)
	if err != nil || avroDriver.GetCategory() != "object_storage" || !avroDriver.GetCapabilities().SupportsSQL {
		t.Fatalf("Ozone Avro driver = %v, error = %v; want registered object-storage SQL adapter", avroDriver, err)
	}
	csvDriver, err := registry.GetDriver(model.DatabaseTypeOzoneCSV)
	if err != nil || csvDriver.GetCategory() != "object_storage" || !csvDriver.GetCapabilities().SupportsSQL {
		t.Fatalf("Ozone CSV driver = %v, error = %v; want registered object-storage SQL adapter", csvDriver, err)
	}
	ossCSVDriver, err := registry.GetDriver(model.DatabaseTypeOSSCSV)
	if err != nil || ossCSVDriver.GetCategory() != "object_storage" || !ossCSVDriver.GetCapabilities().SupportsSQL {
		t.Fatalf("OSS CSV driver = %v, error = %v; want registered object-storage SQL adapter", ossCSVDriver, err)
	}
}

func TestAzureBlobCSVDriverIsRegistered(t *testing.T) {
	registry := NewDriverRegistry()
	driver, err := registry.GetDriver(model.DatabaseTypeAzureBlobCSV)
	if err != nil {
		t.Fatalf("GetDriver(azure_blob_csv) error = %v", err)
	}
	if driver.GetCategory() != "object_storage" || !driver.GetCapabilities().SupportsSQL || !driver.GetCapabilities().SupportsSchemaDiscovery {
		t.Fatalf("Azure Blob CSV driver category/capabilities = %q/%+v", driver.GetCategory(), driver.GetCapabilities())
	}
}

func TestCOSCSVDriverIsRegistered(t *testing.T) {
	registry := NewDriverRegistry()
	driver, err := registry.GetDriver(model.DatabaseTypeTencentCOSCSV)
	if err != nil {
		t.Fatalf("GetDriver(cos_csv) error = %v", err)
	}
	if driver.GetCategory() != "object_storage" || !driver.GetCapabilities().SupportsSQL || !driver.GetCapabilities().SupportsSchemaDiscovery {
		t.Fatalf("COS CSV driver category/capabilities = %q/%+v", driver.GetCategory(), driver.GetCapabilities())
	}
}
