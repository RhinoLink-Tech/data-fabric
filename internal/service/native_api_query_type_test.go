package service

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils"
)

// TestBuildExecutionColumnInfoStandardizesObjectCSVTypes checks that the
// result metadata builder passes common CSV execution types through the
// standardized mapper for the OSS CSV family, instead of returning unknown.
func TestBuildExecutionColumnInfoStandardizesObjectCSVTypes(t *testing.T) {
	qs := &queryService{typeMapper: utils.NewDataTypeMapper()}
	columns := qs.buildExecutionColumnInfo([]execution.Column{
		{Name: "id", Type: "BIGINT"},
		{Name: "score", Type: "DOUBLE"},
	}, model.DatabaseTypeOSSCSV)

	want := []model.StandardizedType{model.TypeBigInt, model.TypeDouble}
	if len(columns) != len(want) {
		t.Fatalf("buildExecutionColumnInfo returned %d columns, want %d", len(columns), len(want))
	}
	for i, column := range columns {
		if column.Type != want[i] {
			t.Errorf("column %q type = %q, want %q", column.Name, column.Type, want[i])
		}
	}
}
