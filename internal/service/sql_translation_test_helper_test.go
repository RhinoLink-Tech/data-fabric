package service

import "github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"

// Service unit tests use the deterministic mock translator unless they are
// explicitly testing the external SQLGlot subprocess.
func newTestSQLTranslationManager() *sql_translator.SQLTranslationManager {
	return sql_translator.NewSQLTranslationManager(false)
}
