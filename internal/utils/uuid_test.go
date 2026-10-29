package utils

import (
	"testing"

	"github.com/google/uuid"
)

func TestIsValidUUID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"canonical", "123e4567-e89b-12d3-a456-426614174000", true},
		{"uppercase", "123E4567-E89B-12D3-A456-426614174000", true},
		{"nil UUID", "00000000-0000-0000-0000-000000000000", true},
		{"empty", "", false},
		{"malformed", "not-a-uuid", false},
		{"truncated", "123e4567-e89b-12d3-a456-42661417400", false},
		{"invalid hex", "123e4567-e89b-12d3-a456-42661417400g", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidUUID(tt.input); got != tt.want {
				t.Errorf("IsValidUUID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateUUID(t *testing.T) {
	got := GenerateUUID()
	parsed, err := uuid.Parse(got)
	if err != nil {
		t.Fatalf("GenerateUUID() = %q, cannot parse UUID: %v", got, err)
	}
	if parsed == uuid.Nil {
		t.Errorf("GenerateUUID() returned the nil UUID")
	}
}

func TestUUIDOrNil(t *testing.T) {
	wantUUID := uuid.UUID{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00}
	tests := []struct {
		name  string
		input string
		want  uuid.UUID
	}{
		{"canonical", "123e4567-e89b-12d3-a456-426614174000", wantUUID},
		{"uppercase", "123E4567-E89B-12D3-A456-426614174000", wantUUID},
		{"nil UUID", "00000000-0000-0000-0000-000000000000", uuid.Nil},
		{"empty", "", uuid.Nil},
		{"invalid", "not-a-uuid", uuid.Nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UUIDOrNil(tt.input); got != tt.want {
				t.Errorf("UUIDOrNil(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMustParseUUID(t *testing.T) {
	wantUUID := uuid.UUID{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00}
	tests := []struct {
		name  string
		input string
		want  uuid.UUID
	}{
		{"canonical", "123e4567-e89b-12d3-a456-426614174000", wantUUID},
		{"uppercase", "123E4567-E89B-12D3-A456-426614174000", wantUUID},
		{"nil UUID", "00000000-0000-0000-0000-000000000000", uuid.Nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MustParseUUID(tt.input); got != tt.want {
				t.Errorf("MustParseUUID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMustParseUUIDPanics(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"malformed", "not-a-uuid"},
		{"invalid hex", "123e4567-e89b-12d3-a456-42661417400g"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered == nil {
					t.Errorf("MustParseUUID(%q) did not panic", tt.input)
				}
			}()
			MustParseUUID(tt.input)
		})
	}
}
