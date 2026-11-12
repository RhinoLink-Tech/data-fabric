package acceptance

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

func TestLargeResultSQLProducesDeterministicNineWayFixture(t *testing.T) {
	query := largeResultSQL("sample")

	if strings.Count(query, "sample.orders AS t") != 9 {
		t.Fatalf("large result SQL should read nine fixture aliases: %s", query)
	}
	if strings.Count(query, " CROSS JOIN ") != 8 {
		t.Fatalf("large result SQL should contain eight cross joins: %s", query)
	}
	if !strings.HasPrefix(query, "SELECT REPEAT('x', 4096) AS payload FROM sample.orders AS t0") {
		t.Fatalf("large result SQL has an unexpected projection or first source: %s", query)
	}
	if largeResultRows != 3*3*3*3*3*3*3*3*3 {
		t.Fatalf("fixture cardinality = %d, want 3^9", largeResultRows)
	}
}

func TestEncodedBudgetPageBytesIsPositiveAndDeterministic(t *testing.T) {
	page := batch{
		Columns: []column{{Name: "payload", Type: "VARCHAR"}},
		Entries: [][]any{{"x", 2}},
	}
	first, err := encodedBudgetPageBytes(page)
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodedBudgetPageBytes(page)
	if err != nil {
		t.Fatal(err)
	}
	if first <= 0 || first != second {
		t.Fatalf("encoded page bytes are not positive and deterministic: first=%d second=%d", first, second)
	}
}

func TestAssertGatewayMemoryCaps(t *testing.T) {
	const limit = int64(512 << 20)
	valid := reference.ResourceSnapshot{Containers: []reference.ContainerResource{
		{Name: "df-data-fabric-api-1", MemoryBytes: 100, MemoryLimitBytes: uint64(limit)},
		{Name: "df-data-fabric-mcp-1", MemoryBytes: 200, MemoryLimitBytes: uint64(limit)},
	}}
	if err := assertGatewayMemoryCaps(valid, limit); err != nil {
		t.Fatalf("valid gateway resource snapshot rejected: %v", err)
	}

	tests := []struct {
		name     string
		snapshot reference.ResourceSnapshot
		limit    int64
	}{
		{name: "non-positive configured limit", snapshot: valid},
		{name: "missing MCP process", snapshot: reference.ResourceSnapshot{Containers: valid.Containers[:1]}, limit: limit},
		{name: "mismatched cgroup limit", snapshot: reference.ResourceSnapshot{Containers: []reference.ContainerResource{
			{Name: "df-data-fabric-api-1", MemoryLimitBytes: uint64(limit - 1)},
			{Name: "df-data-fabric-mcp-1", MemoryLimitBytes: uint64(limit)},
		}}, limit: limit},
		{name: "usage over cap", snapshot: reference.ResourceSnapshot{Containers: []reference.ContainerResource{
			{Name: "df-data-fabric-api-1", MemoryBytes: uint64(limit + 1), MemoryLimitBytes: uint64(limit)},
			{Name: "df-data-fabric-mcp-1", MemoryLimitBytes: uint64(limit)},
		}}, limit: limit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := assertGatewayMemoryCaps(test.snapshot, test.limit); err == nil {
				t.Fatal("assertGatewayMemoryCaps() unexpectedly succeeded")
			}
		})
	}
}

func TestGatewayServiceName(t *testing.T) {
	for _, test := range []struct {
		container string
		want      string
	}{
		{container: "df-reference-api-data-fabric-api-1", want: "data-fabric-api"},
		{container: "df-reference-mcp-data-fabric-mcp-1", want: "data-fabric-mcp"},
		{container: "df-reference-sample-source-1", want: ""},
	} {
		if got := gatewayServiceName(test.container); got != test.want {
			t.Errorf("gatewayServiceName(%q) = %q, want %q", test.container, got, test.want)
		}
	}
}
