// Package sqlanalysis contains the data returned by dialect-aware SQL parsing.
package sqlanalysis

// Analysis reports security-relevant facts extracted from a parsed SQL AST.
// It intentionally contains no SQL text or literal values.
type Analysis struct {
	StatementCount int      `json:"statement_count"`
	IsQuery        bool     `json:"is_query"`
	HasMutation    bool     `json:"has_mutation"`
	HasSelectInto  bool     `json:"has_select_into"`
	HasLocking     bool     `json:"has_locking"`
	Functions      []string `json:"functions"`
}
