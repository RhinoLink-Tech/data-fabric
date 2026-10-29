package service

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils"
)

// The streaming path routes covered families through the shared
// DataTypeMapper, so the diverging rows measured in the D14 validation run
// now report identical standardized types in both execution modes (#260).
func TestStreamingTypeMappingMatchesPaginationMapper(t *testing.T) {
	service := &streamingService{}
	mapper := utils.NewDataTypeMapper()
	cases := []struct {
		dbType model.DatabaseType
		column string
	}{
		{model.DatabaseTypeMySQL, "bigint"},
		{model.DatabaseTypeMySQL, "text"},
		{model.DatabaseTypeMySQL, "datetime"},
		{model.DatabaseTypePostgreSQL, "text"},
		{model.DatabaseTypePostgreSQL, "timestamp"},
	}
	for _, tc := range cases {
		name := string(tc.dbType) + "/" + tc.column
		t.Run(name, func(t *testing.T) {
			want := mapper.MapToStandardType(tc.dbType, tc.column, false)
			got := service.mapDatabaseTypeToStandard(tc.column, tc.dbType, false)
			if got != want {
				t.Fatalf("mapDatabaseTypeToStandard(%q, %s) = %q, pagination mapper reports %q", tc.column, tc.dbType, got, want)
			}
			expected := map[string]model.StandardizedType{
				"mysql/bigint": model.TypeBigInt, "mysql/text": model.TypeText,
				"mysql/datetime": model.TypeDateTime, "postgresql/text": model.TypeText,
				"postgresql/timestamp": model.TypeTimestamp,
			}[name]
			if got != expected {
				t.Fatalf("%s = %q, want the unified %q", name, got, expected)
			}
		})
	}
}

// Families the shared mapper does not cover (Snowflake, BigQuery,
// Databricks, Redshift, and unregistered types) keep today's generic
// matcher; unification must not turn them into TypeUnknown (#260).
func TestStreamingTypeMappingKeepsGenericFallbackForUncoveredFamilies(t *testing.T) {
	service := &streamingService{}
	if got := service.mapDatabaseTypeToStandard("varchar", model.DatabaseTypeSnowflake, false); got != model.TypeString {
		t.Fatalf("snowflake varchar = %q, want string via the generic fallback", got)
	}
	if got := service.mapDatabaseTypeToStandard("double", model.DatabaseTypeBigQuery, false); got != model.TypeDouble {
		t.Fatalf("bigquery double = %q, want double via the generic fallback", got)
	}
}
