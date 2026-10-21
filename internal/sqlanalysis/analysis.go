// Package sqlanalysis contains the data returned by dialect-aware SQL parsing.
package sqlanalysis

// Analysis reports security-relevant facts extracted from a parsed SQL AST.
// It intentionally contains no SQL text or literal values.
type Analysis struct {
	StatementCount   int              `json:"statement_count"`
	IsQuery          bool             `json:"is_query"`
	HasMutation      bool             `json:"has_mutation"`
	HasSelectInto    bool             `json:"has_select_into"`
	HasLocking       bool             `json:"has_locking"`
	Functions        []string         `json:"functions"`
	Tables           []TableReference `json:"tables"`
	UnresolvedTables bool             `json:"unresolved_tables"`
}

// TableReference is a physical table identified by the dialect-aware parser.
// CTE names and derived-table aliases are intentionally excluded.
type TableReference struct {
	Catalog string `json:"catalog,omitempty"`
	Schema  string `json:"schema,omitempty"`
	Table   string `json:"table"`
	// Unqualified is true when SQL omitted both catalog and schema. A datasource
	// default may resolve it; otherwise policy matching must not treat missing
	// namespace as an explicitly empty namespace.
	Unqualified bool `json:"unqualified,omitempty"`
}
