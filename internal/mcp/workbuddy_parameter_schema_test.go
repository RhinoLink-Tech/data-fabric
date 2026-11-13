package mcp

import (
	"encoding/json"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// The advertised schema must accept the supported bind values and reject
// compound values. WorkBuddy rejected a valid numeric value under oneOf.
func TestExecuteParameterSchemaAcceptsScalarValues(t *testing.T) {
	tool := contractServer(true).server.ListTools()["execute_sql_query"].Tool
	data, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err = json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://example.invalid/execute-schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://example.invalid/execute-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range [][]any{{nil, true, float64(1), "text"}, {float64(1)}, {false}} {
		if err = schema.Validate(map[string]any{"datasource_id": "id", "sql": "SELECT ", "parameters": values}); err != nil {
			t.Fatalf("scalar values rejected: %v", err)
		}
	}
	for _, value := range []any{map[string]any{"x": "y"}, []any{float64(1)}} {
		if err = schema.Validate(map[string]any{"datasource_id": "id", "sql": "SELECT ", "parameters": []any{value}}); err == nil {
			t.Fatalf("compound bind value accepted: %T", value)
		}
	}
}
