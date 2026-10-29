package service

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Query-engine types map through the shared DataTypeMapper so streaming
// reports exactly what pagination reports for the same column (#260).
func TestStreamingQueryEngineTypeMapping(t *testing.T) {
	service := &streamingService{}
	tests := []struct {
		typeName string
		want     model.StandardizedType
	}{
		{typeName: "bigint", want: model.TypeBigInt},
		{typeName: "integer", want: model.TypeInteger},
		{typeName: "timestamp with time zone", want: model.TypeTimestamp},
		{typeName: "time with time zone", want: model.TypeTime},
		{typeName: "array(varchar)", want: model.TypeArray},
	}

	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			if got := service.mapDatabaseTypeToStandard(tt.typeName, model.DatabaseTypeTrino, false); got != tt.want {
				t.Errorf("mapDatabaseTypeToStandard(%q, trino) = %q, want %q", tt.typeName, got, tt.want)
			}
		})
	}
}
