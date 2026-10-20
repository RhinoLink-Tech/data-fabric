package model

import (
	"strings"
	"testing"
	"time"
)

const scanValidJSON = `{"host":"db.internal","port":3306,"database":"sample","username":"reader","ssl":true}`

// TestDataSourceConfigScanHandlesDriverValueTypes locks #122: every SQL
// value type a driver may return is handled without panicking — string and
// []byte parse, nil is a no-op, malformed JSON errors, and anything else
// returns a descriptive error instead of hitting a type assertion.
func TestDataSourceConfigScanHandlesDriverValueTypes(t *testing.T) {
	tests := []struct {
		name    string
		value   interface{}
		wantErr bool
	}{
		{name: "nil leaves the config untouched", value: nil},
		{name: "string JSON", value: scanValidJSON},
		{name: "[]byte JSON", value: []byte(scanValidJSON)},
		{name: "string with malformed JSON", value: `{"host":`, wantErr: true},
		{name: "[]byte with malformed JSON", value: []byte(`{"host":`), wantErr: true},
		{name: "int64 returns an error", value: int64(42), wantErr: true},
		{name: "float64 returns an error", value: 42.0, wantErr: true},
		{name: "bool returns an error", value: true, wantErr: true},
		{name: "time.Time returns an error", value: time.Now(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config DataSourceConfig
			err := config.Scan(tt.value)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Scan(%T) = nil, want error", tt.value)
				}
				if strings.Contains(err.Error(), "panic") {
					t.Errorf("error mentions panic: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Scan(%T) error = %v", tt.value, err)
			}
		})
	}
}

// TestDataSourceConfigScanRejectsUnsupportedTypeWithName makes the
// descriptive-error requirement concrete: the message names the offending
// type so a driver change is debuggable from the logs.
func TestDataSourceConfigScanRejectsUnsupportedTypeWithName(t *testing.T) {
	var config DataSourceConfig
	err := config.Scan(int64(42))
	if err == nil {
		t.Fatal("Scan(int64) = nil, want error")
	}
	if !strings.Contains(err.Error(), "int64") {
		t.Errorf("error = %v, want it to name the type int64", err)
	}
}

// TestDataSourceConfigScanParsesJSONContent verifies the happy path actually
// decodes, not just that it returns without error.
func TestDataSourceConfigScanParsesJSONContent(t *testing.T) {
	var config DataSourceConfig
	if err := config.Scan([]byte(scanValidJSON)); err != nil {
		t.Fatalf("Scan([]byte) error = %v", err)
	}
	if config.Host != "db.internal" || config.Port != 3306 || config.Database != "sample" {
		t.Errorf("scanned config = %+v, want host db.internal / port 3306 / database sample", config)
	}
	if !config.SSL {
		t.Error("scanned config lost the ssl flag")
	}
}

// TestDataSourceConfigValueScanRoundTrip keeps Value and Scan symmetric
// (#122): what a row stores must read back as the same configuration.
func TestDataSourceConfigValueScanRoundTrip(t *testing.T) {
	original := DataSourceConfig{
		Host:     "db.internal",
		Port:     3306,
		Database: "sample",
		Username: "reader",
		SSL:      true,
		AdditionalProps: map[string]interface{}{
			"enforce_read_only": true,
			"note":              "round trip",
		},
	}

	stored, err := original.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}

	var restored DataSourceConfig
	if err := restored.Scan(stored); err != nil {
		t.Fatalf("Scan(Value()) error = %v", err)
	}

	if restored.Host != original.Host || restored.Port != original.Port ||
		restored.Database != original.Database || restored.Username != original.Username ||
		restored.SSL != original.SSL {
		t.Errorf("round trip lost fields: got %+v, want %+v", restored, original)
	}
	if v, ok := restored.AdditionalProps["enforce_read_only"].(bool); !ok || !v {
		t.Errorf("round trip lost additionalProps: %+v", restored.AdditionalProps)
	}
	if v, ok := restored.AdditionalProps["note"].(string); !ok || v != "round trip" {
		t.Errorf("round trip lost additionalProps strings: %+v", restored.AdditionalProps)
	}
}
