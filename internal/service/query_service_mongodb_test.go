package service

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
)

func TestMongoDBTranslationTargetsAcceptedMySQLSubset(t *testing.T) {
	service := &queryService{sqlTranslator: sql_translator.NewSQLTranslationManager(false)}
	translated, err := service.translateSQLIfNeeded(
		context.Background(),
		"SELECT \"name\" FROM customers",
		"postgres",
		"mongodb",
	)
	if err != nil {
		t.Fatalf("translateSQLIfNeeded() error = %v", err)
	}
	backtick := string(rune(96))
	if !strings.Contains(translated, backtick+"name"+backtick) {
		t.Fatalf("translated SQL = %q, want MySQL-compatible quoted identifier", translated)
	}

	translated, err = service.translateSQLIfNeeded(context.Background(), "SELECT name FROM customers", "mysql", "mongodb")
	if err != nil {
		t.Fatalf("same accepted dialect should not need translation: %v", err)
	}
	if translated != "" {
		t.Fatalf("same accepted dialect returned translated SQL %q, want empty marker", translated)
	}
}
