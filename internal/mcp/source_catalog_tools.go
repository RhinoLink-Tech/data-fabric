package mcp

import (
	"context"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/mark3labs/mcp-go/mcp"
)

// sourceTypeFormat describes format-bearing connector IDs only. The empty
// string means that this connector is not a single named file format.
func sourceTypeFormat(category, name string) string {
	if category == "table_format" {
		switch name {
		case "iceberg", "hudi":
			return name
		case "delta_lake":
			return "delta"
		}
	}
	if category != "object_storage" && category != "file_system" {
		return ""
	}
	if strings.HasSuffix(name, "_parquet_compressed") {
		return "parquet"
	}
	for _, format := range []string{"csv", "parquet", "json", "avro", "orc", "text", "delta", "iceberg", "hudi"} {
		if strings.HasSuffix(name, "_"+format) {
			return format
		}
	}
	return ""
}

func (m *MCPServer) handleListSourceTypes(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	for key, value := range args {
		switch key {
		case "category", "format", "type":
			if _, ok := value.(string); !ok {
				return mcp.NewToolResultError("filters must be strings"), nil
			}
		case "include_details":
			if _, ok := value.(bool); !ok {
				return mcp.NewToolResultError("include_details must be a boolean"), nil
			}
		default:
			return mcp.NewToolResultError("unknown filter"), nil
		}
	}
	category, _ := args["category"].(string)
	format, _ := args["format"].(string)
	sourceType, _ := args["type"].(string)
	details, _ := args["include_details"].(bool)
	if category != strings.TrimSpace(category) || format != strings.TrimSpace(format) || sourceType != strings.TrimSpace(sourceType) {
		return mcp.NewToolResultError("filters must not contain surrounding whitespace"), nil
	}
	types := make([]map[string]any, 0)
	for _, entry := range database.SupportCatalog(database.GetDriverRegistry()) {
		f := sourceTypeFormat(entry.Category, entry.Type)
		if category != "" && entry.Category != category || format != "" && f != format || sourceType != "" && entry.Type != sourceType {
			continue
		}
		item := map[string]any{"type": entry.Type, "category": entry.Category, "status": entry.Status, "end_to_end_verified": entry.EndToEndVerified}
		if details {
			item["version_policy"] = entry.VersionPolicy
			item["limitations"] = entry.Limitations
			item["operations"] = entry.Operations
			item["authentication_disclosure"] = entry.AuthenticationDisclosure
			item["authentication_methods"] = entry.AuthenticationMethods
			item["compatibility_mode"] = entry.CompatibilityMode
		}
		if f != "" {
			item["format"] = f
		}
		if len(entry.VerifiedServerVersions) > 0 {
			item["verified_server_versions"] = entry.VerifiedServerVersions
		}
		if entry.SupportedVersionRange != "" {
			item["supported_version_range"] = entry.SupportedVersionRange
		}
		types = append(types, item)
	}
	return mcpTablePolicyJSON(map[string]any{"source_types": types, "count": len(types), "meaning": "Registered connector types in this server build; these are not configured data sources. Verification is version- and scenario-specific; set include_details to true for operations, limitations and authentication details."})
}
