package database

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestRegistryAcceptsNativeDriverWithoutSQLConnectionMethods(t *testing.T) {
	registry := NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseType("native_test"), nativeOnlyTestDriver{})
	driver, err := registry.GetDriver(model.DatabaseType("native_test"))
	if err != nil {
		t.Fatalf("GetDriver() error = %v", err)
	}
	if _, ok := driver.(drivers.SQLDriver); ok {
		t.Fatal("native-only driver unexpectedly implements the SQL connection extension")
	}

	pool := NewConnectionPool(registry, nil, nil, false)
	executor, supported, err := pool.GetNativeSQLAPIExecutor(context.Background(), &model.DataSource{
		Type: model.DatabaseType("native_test"),
	})
	if err != nil {
		t.Fatalf("GetNativeAPIExecutor() error = %v", err)
	}
	if !supported || executor == nil || executor.Capabilities().Mode != execution.ModeNativeAPI {
		t.Fatalf("native executor = %#v, supported = %t", executor, supported)
	}
}

type nativeOnlyTestDriver struct{}

func (nativeOnlyTestDriver) ValidateDSN(string) error                { return nil }
func (nativeOnlyTestDriver) GetDefaultPort() int                     { return 443 }
func (nativeOnlyTestDriver) BuildDSN(*model.DataSourceConfig) string { return "https://native.test" }
func (nativeOnlyTestDriver) GetDatabaseTypeName() string             { return "native_test" }
func (nativeOnlyTestDriver) GetDriverName() string                   { return "native_test" }
func (nativeOnlyTestDriver) GetCategory() drivers.DriverCategory     { return drivers.CategoryNoSQL }
func (nativeOnlyTestDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true}
}
func (nativeOnlyTestDriver) ConfigureAuth(interface{}) error { return nil }
func (nativeOnlyTestDriver) OpenNativeAPIExecutor(context.Context, *model.DataSourceConfig) (execution.NativeAPIExecutor, error) {
	return nativeOnlyTestExecutor{}, nil
}

type nativeOnlyTestExecutor struct{}

func (nativeOnlyTestExecutor) Capabilities() execution.Capabilities {
	return execution.Capabilities{Mode: execution.ModeNativeAPI}
}
func (nativeOnlyTestExecutor) ExecuteNativeAPI(context.Context, execution.NativeAPIRequest) (execution.ResultStream, error) {
	return nil, nil
}

var _ drivers.Driver = nativeOnlyTestDriver{}
var _ drivers.NativeAPIExecutorProvider = nativeOnlyTestDriver{}
