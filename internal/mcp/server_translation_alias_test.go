package mcp

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
)

func TestTranslationDialectAliasesMCP(t *testing.T) {
	srv := NewMCPServerWithTableAuthorizer(nil, nil, &stubTableDescriber{}, nil, sql_translator.NewSQLTranslationManager(false))
	for _, tc := range []struct {
		source, target string
		valid          bool
	}{
		{" MySQL ", "PostgreSQL", true}, {"mysql", "postgres", true}, {" MySQL ", " TSQL ", true},
		{"mysql", "not_a_dialect", false}, {"mariadb", "mysql", false}, {" ", "mysql", false},
	} {
		t.Run(tc.source+"_"+tc.target, func(t *testing.T) {
			request := callToolRequest("translate_sql", map[string]any{"sql": "SELECT 1", "source_dialect": tc.source, "target_dialect": tc.target})
			result, err := srv.handleTranslateSQL(context.Background(), request)
			if (err == nil && result != nil) != tc.valid {
				t.Fatalf("unexpected translation outcome: %v", err)
			}
		})
	}
}
