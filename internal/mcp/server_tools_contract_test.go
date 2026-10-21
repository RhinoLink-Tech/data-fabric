package mcp

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
)

// toolContract is the documented MCP tool contract. The table is sourced from
// docs/mcp-tools.md (the registry of record) and pinned against what
// registerTools actually registers, so renaming a tool, dropping a required
// field, or forgetting to document a new tool fails here (#181).
type toolContract struct {
	required    []string
	properties  []string
	conditional bool // only registered under some configurations
}

var documentedToolContract = map[string]toolContract{
	"list_data_sources":       {properties: []string{}},
	"execute_sql_query":       {required: []string{"datasource_id", "sql"}, properties: []string{"datasource_id", "sql", "source_dialect", "parameters", "batch_size", "timeout"}},
	"fetch_next_batch":        {required: []string{"query_id", "slug", "token"}, properties: []string{"query_id", "slug", "token", "batch_size"}},
	"get_data_source_info":    {required: []string{"datasource_id"}, properties: []string{"datasource_id"}},
	"validate_sql_query":      {required: []string{"datasource_id", "sql"}, properties: []string{"datasource_id", "sql", "source_dialect"}},
	"list_tables":             {required: []string{"datasource_id"}, properties: []string{"datasource_id"}},
	"describe_table":          {required: []string{"datasource_id", "table_name"}, properties: []string{"datasource_id", "table_name", "schema"}, conditional: true},
	"translate_sql":           {required: []string{"sql", "source_dialect", "target_dialect"}, properties: []string{"sql", "source_dialect", "target_dialect"}, conditional: true},
	"list_supported_dialects": {properties: []string{}, conditional: true},
}

func contractServer(translationEnabled bool) *MCPServer {
	// The repository and query service stay nil: the contract tests exercise
	// registration only, and registerTools never touches them. The enabled
	// variant uses the mock translator (no Python); the disabled variant is a
	// runtime manager configured off — construction has no side effects.
	var translator *sql_translator.SQLTranslationManager
	if translationEnabled {
		translator = sql_translator.NewSQLTranslationManager(false)
	} else {
		translator = sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: false})
	}
	return NewMCPServerWithTableAuthorizer(nil, nil, &stubTableDescriber{}, nil, translator)
}

func registeredToolNames(s *MCPServer) []string {
	names := make([]string, 0, len(s.server.ListTools()))
	for name := range s.server.ListTools() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// equalIgnoringOrder compares two string slices as sets.
func equalIgnoringOrder(a, b []string) bool {
	sortedA := slices.Clone(a)
	slices.Sort(sortedA)
	return slices.Equal(sortedA, sortedCopy(b))
}

func sortedCopy(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

// With translation enabled and a table describer wired — the full
// configuration both server modes run today — all nine documented tools are
// registered and each carries exactly the documented required fields and
// input properties (#181).
func TestRegisteredToolsMatchContractWithTranslationEnabled(t *testing.T) {
	srv := contractServer(true)

	want := make([]string, 0, len(documentedToolContract))
	for name := range documentedToolContract {
		want = append(want, name)
	}
	slices.Sort(want)

	if got := registeredToolNames(srv); !slices.Equal(got, want) {
		t.Fatalf("registered tools = %v, want %v", got, want)
	}

	for name, tool := range srv.server.ListTools() {
		contract := documentedToolContract[name]
		if got := tool.Tool.InputSchema.Required; !equalIgnoringOrder(got, contract.required) {
			t.Errorf("%s required = %v, want %v", name, got, contract.required)
		}

		got := make([]string, 0, len(tool.Tool.InputSchema.Properties))
		for property := range tool.Tool.InputSchema.Properties {
			got = append(got, property)
		}
		slices.Sort(got)
		if !slices.Equal(got, sortedCopy(contract.properties)) {
			t.Errorf("%s properties = %v, want %v", name, got, contract.properties)
		}
	}
}

// With translation disabled the two dialect tools disappear; the other seven
// — including conditionally documented describe_table — stay registered.
func TestRegisteredToolsMatchContractWithTranslationDisabled(t *testing.T) {
	srv := contractServer(false)

	var want []string
	for name := range documentedToolContract {
		if name == "translate_sql" || name == "list_supported_dialects" {
			continue
		}
		want = append(want, name)
	}
	slices.Sort(want)

	if got := registeredToolNames(srv); !slices.Equal(got, want) {
		t.Fatalf("registered tools without translation = %v, want %v", got, want)
	}
}

// Every registered tool must be documented: this reads the real doc file, not
// this file's table, so implementation-first changes cannot pass unnoticed
// (#181).
func TestEveryRegisteredToolIsDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/mcp-tools.md")
	if err != nil {
		t.Fatalf("read docs/mcp-tools.md: %v", err)
	}

	for _, name := range registeredToolNames(contractServer(true)) {
		if !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("tool %q is registered but not documented in docs/mcp-tools.md", name)
		}
	}
}

// Every documented tool must be registered in its registering configuration:
// conditional tools are checked with translation enabled, unconditional ones
// in both (#181).
func TestEveryDocumentedToolIsRegistered(t *testing.T) {
	full := registeredToolNames(contractServer(true))
	lean := registeredToolNames(contractServer(false))

	for name, contract := range documentedToolContract {
		if !slices.Contains(full, name) {
			t.Errorf("documented tool %q is not registered (translation enabled)", name)
		}
		if contract.conditional {
			continue
		}
		if !slices.Contains(lean, name) {
			t.Errorf("documented tool %q is not registered with translation disabled", name)
		}
	}
}
