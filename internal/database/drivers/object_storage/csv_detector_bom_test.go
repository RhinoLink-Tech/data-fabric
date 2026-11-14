package object_storage

import (
	"strings"
	"testing"
)

func TestDetectSchemaStripsLeadingUTF8BOM(t *testing.T) {
	bom := "\xEF\xBB\xBF"

	tests := []struct {
		name string
		data string
	}{
		{name: "unquoted header with BOM", data: bom + "order_id,amount\n1,10.5\n2,20.5\n"},
		{name: "quoted header with BOM", data: bom + "\"order_id\",\"amount\"\n1,10.5\n2,20.5\n"},
		{name: "no BOM", data: "order_id,amount\n1,10.5\n2,20.5\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewCSVSchemadDetector(nil)
			schema, err := detector.DetectSchema([]byte(tt.data))
			if err != nil {
				t.Fatalf("DetectSchema returned error: %v", err)
			}
			if len(schema.Columns) != 2 {
				t.Fatalf("expected 2 columns, got %d", len(schema.Columns))
			}
			if got := schema.Columns[0].Name; got != "order_id" {
				t.Errorf("first column name = %q, want %q", got, "order_id")
			}
			if got := schema.Columns[1].Name; got != "amount" {
				t.Errorf("second column name = %q, want %q", got, "amount")
			}
			if got := schema.HeaderRow[0]; got != "order_id" {
				t.Errorf("header row first field = %q, want %q", got, "order_id")
			}
			for _, col := range schema.Columns {
				if strings.ContainsRune(col.Name, '\ufeff') {
					t.Errorf("column name %q still contains BOM", col.Name)
				}
			}
		})
	}
}
