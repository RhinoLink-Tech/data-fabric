package security

import (
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// TestValidatorAcceptsLegitimateQueries locks the #19 fix: ordinary read-only
// SQL from the core MySQL/PostgreSQL subset — IN lists, amounts, dates, and
// keyword-shaped text — must pass validation instead of tripping heuristics
// that read string literals as SQL structure.
func TestValidatorAcceptsLegitimateQueries(t *testing.T) {
	validator := NewSQLValidator(10000)

	legitimate := []struct {
		name string
		sql  string
	}{
		{"baseline amount filter", "SELECT id FROM orders WHERE amount >= 100"},
		{"amount with repeated digits", "SELECT id FROM orders WHERE amount >= 1000"},
		{"IN list of statuses", "SELECT id FROM orders WHERE status IN ('paid', 'shipped')"},
		{"keyword-shaped text as label", "SELECT 'DROP' AS status_label"},
		{"date range", "SELECT id FROM orders WHERE created_at BETWEEN '2026-01-01' AND '2026-12-31'"},
		{"keyword and dashes inside a literal", "SELECT note FROM orders WHERE note = 'update -- pending'"},
		{"decimal with repeated zeros", "SELECT customer, amount FROM orders WHERE amount > 999.000"},
		{"two-way union", "SELECT id FROM orders UNION SELECT id FROM archive_orders"},
		{"three-way union", "SELECT id FROM orders UNION SELECT id FROM archive_orders UNION SELECT id FROM foreign_orders"},
		{"statement text inside a literal", "SELECT id FROM orders WHERE note = 'a;b; DROP TABLE users'"},
		{"escaped quote in literal", "SELECT 'it''s fine' AS note"},
	}

	for _, tt := range legitimate {
		t.Run(tt.name, func(t *testing.T) {
			for _, dbType := range []model.DatabaseType{model.DatabaseTypeMySQL, model.DatabaseTypePostgreSQL} {
				if err := validator.ValidateStatementForDataSource(tt.sql, dbType); err != nil {
					t.Errorf("ValidateStatementForDataSource(%q, %s) = %v, want acceptance", tt.sql, dbType, err)
				}
			}
		})
	}
}

// TestValidatorStillRejectsInjectionShapedStructure keeps the defense side of
// #19 honest: masking literals must not open a path for tautology probes,
// comments outside literals, or control characters.
func TestValidatorStillRejectsInjectionShapedStructure(t *testing.T) {
	validator := NewSQLValidator(10000)

	mustReject := []struct {
		name string
		sql  string
		want error
	}{
		{"or tautology", "SELECT * FROM users WHERE user_id = 1 OR 1=1", ErrSQLInjection},
		{"literal tautology probe", "SELECT * FROM orders WHERE 'x'='x'", ErrSQLInjection},
		{"line comment outside literals", "SELECT 1 -- trailing", ErrSQLInjection},
		{"block comment outside literals", "SELECT /* hint */ 1", ErrSQLInjection},
		{"control characters in literal", "SELECT * FROM t WHERE name = '\x01\x02\x03\x04\x05\x06'", ErrSQLInjection},
	}

	for _, tt := range mustReject {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateStatementForDataSource(tt.sql, model.DatabaseTypeMySQL)
			if !errors.Is(err, tt.want) {
				t.Errorf("ValidateStatementForDataSource(%q) = %v, want %v", tt.sql, err, tt.want)
			}
		})
	}
}

// TestMaskStringLiterals pins the masking contract the text heuristics rely
// on: whole literals become one placeholder, escapes stay inside, quoted
// identifiers are untouched.
func TestMaskStringLiterals(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no literals", "SELECT id FROM orders WHERE amount >= 1000", "SELECT id FROM orders WHERE amount >= 1000"},
		{"simple literal", "SELECT 'DROP' AS s", "SELECT ? AS s"},
		{"IN list", "IN ('paid', 'shipped')", "IN (?, ?)"},
		{"doubled quote escape", "SELECT 'it''s' AS s", "SELECT ? AS s"},
		{"backslash escape", "WHERE x = 'a\\'b'", "WHERE x = ?"},
		{"trailing backslash", "WHERE x = 'abc\\", "WHERE x = ?"},
		{"unterminated literal", "WHERE x = 'abc", "WHERE x = ?"},
		{"quoted identifiers untouched", "SELECT \"note\", `note` FROM t", "SELECT \"note\", `note` FROM t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskStringLiterals(tt.in); got != tt.want {
				t.Errorf("maskStringLiterals(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
