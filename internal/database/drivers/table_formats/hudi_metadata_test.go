package table_formats

import (
	"testing"
	"time"
)

// Instant times are read from table metadata on disk, so a truncated or empty
// value is an ordinary input rather than an impossible one. ParseInstantTime
// used to slice its argument at a fixed offset before parsing, which panicked
// the caller instead of reporting the bad value.
func TestParseInstantTime(t *testing.T) {
	parser := NewHudiMetadataParser()

	tests := []struct {
		name        string
		instantTime string
		want        time.Time
		wantErr     bool
	}{
		{
			name:        "millisecond precision",
			instantTime: "20240101153045123",
			want:        time.Date(2024, 1, 1, 15, 30, 45, 123_000_000, time.UTC),
		},
		{
			name:        "zero milliseconds",
			instantTime: "20240101153045000",
			want:        time.Date(2024, 1, 1, 15, 30, 45, 0, time.UTC),
		},
		{
			name:        "second precision",
			instantTime: "20240101153045",
			want:        time.Date(2024, 1, 1, 15, 30, 45, 0, time.UTC),
		},
		{name: "empty", instantTime: "", wantErr: true},
		{name: "truncated", instantTime: "2024010115", wantErr: true},
		{name: "one digit short", instantTime: "2024010115304", wantErr: true},
		{name: "one digit long", instantTime: "2024010115304512", wantErr: true},
		{name: "non-digit milliseconds", instantTime: "20240101153045abc", wantErr: true},
		{name: "negative millisecond suffix", instantTime: "20240101153045-10", wantErr: true},
		{name: "positive millisecond suffix", instantTime: "20240101153045+10", wantErr: true},
		{name: "non-digit seconds", instantTime: "20240101153O45123", wantErr: true},
		{name: "impossible month", instantTime: "20241301153045123", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseInstantTime(%q) panicked: %v", tt.instantTime, r)
				}
			}()

			got, err := parser.ParseInstantTime(tt.instantTime)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseInstantTime(%q) = %v, want an error", tt.instantTime, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseInstantTime(%q) returned error: %v", tt.instantTime, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("ParseInstantTime(%q) = %v, want %v", tt.instantTime, got, tt.want)
			}
		})
	}
}

// FormatInstantTime and ParseInstantTime describe the same format, so anything
// one writes the other has to read back unchanged.
func TestInstantTimeRoundTrip(t *testing.T) {
	parser := NewHudiMetadataParser()

	for _, want := range []time.Time{
		time.Date(2024, 1, 1, 15, 30, 45, 123_000_000, time.UTC),
		time.Date(2024, 2, 29, 0, 0, 0, 7_000_000, time.UTC),
		time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
	} {
		formatted := parser.FormatInstantTime(want)
		got, err := parser.ParseInstantTime(formatted)
		if err != nil {
			t.Fatalf("ParseInstantTime(%q) returned error: %v", formatted, err)
		}
		if !got.Equal(want) {
			t.Errorf("round trip of %v through %q = %v", want, formatted, got)
		}
	}
}
