//go:build datafabric_core

package database

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestCoreRegistryAndCatalogExposeOnlySelectedTypes(t *testing.T) {
	registry := NewDriverRegistry()
	allowed := map[model.DatabaseType]bool{model.DatabaseTypeMySQL: true, model.DatabaseTypeMariaDB: true, model.DatabaseTypePostgreSQL: true, model.DatabaseTypeTrino: true}
	found := registry.ListDrivers()
	if len(found) != len(allowed) {
		t.Fatalf("core registry has %d types, want %d: %v", len(found), len(allowed), found)
	}
	for _, dbType := range found {
		if !allowed[dbType] {
			t.Fatalf("unexpected type %s", dbType)
		}
		driver, err := registry.GetDriver(dbType)
		if err != nil || driver == nil {
			t.Fatalf("type %s: %v", dbType, err)
		}
	}
	for _, excluded := range []model.DatabaseType{model.DatabaseTypeApacheDruid, model.DatabaseTypeClickHouse, model.DatabaseTypeMongoDB, model.DatabaseTypeS3CSV} {
		if registry.IsSupported(excluded) {
			t.Fatalf("excluded type %s is registered", excluded)
		}
		if _, err := registry.GetDriver(excluded); err == nil {
			t.Fatalf("excluded type %s returned a driver", excluded)
		}
	}
	catalog := SupportCatalog(registry)
	if len(catalog) != len(allowed) {
		t.Fatalf("catalog has %d entries, want %d", len(catalog), len(allowed))
	}
	for _, entry := range catalog {
		if !allowed[model.DatabaseType(entry.Type)] || !entry.Registered {
			t.Fatalf("unexpected catalog entry %+v", entry)
		}
	}
}
