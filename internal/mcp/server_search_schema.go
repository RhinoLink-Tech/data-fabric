package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

const (
	defaultSchemaSearchLimit = 10
	maxSchemaSearchLimit     = 20
	maxSchemaSearchScan      = 100
	maxSchemaSearchQuerySize = 128
	maxSchemaSearchColumns   = 5
)

type schemaSearchResponse struct {
	DataSourceID string              `json:"datasource_id"`
	Query        string              `json:"query"`
	Matches      []schemaSearchMatch `json:"matches"`
	Ambiguous    bool                `json:"ambiguous"`
	Truncated    bool                `json:"truncated"`
}

type schemaSearchMatch struct {
	Catalog       string   `json:"catalog,omitempty"`
	Schema        string   `json:"schema,omitempty"`
	Table         string   `json:"table"`
	Type          string   `json:"type,omitempty"`
	MatchedFields []string `json:"matched_fields"`
	Columns       []string `json:"matched_columns,omitempty"`
}

type schemaSearchCandidate struct {
	sourceID  string
	identity  metadata.TableIdentity
	reference sqlanalysis.TableReference
}

// handleSearchSchema returns only a bounded set of permitted table identities.
// It searches table/schema names first, then visible column names and comments.
func (m *MCPServer) handleSearchSchema(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dataSourceID := strings.TrimSpace(mcp.ParseString(request, "datasource_id", ""))
	query := strings.TrimSpace(mcp.ParseString(request, "query", ""))
	if dataSourceID == "" {
		return mcp.NewToolResultError("datasource_id is required"), nil
	}
	querySize := utf8.RuneCountInString(query)
	if querySize < 2 || querySize > maxSchemaSearchQuerySize {
		return mcp.NewToolResultError("query must contain 2 to 128 characters"), nil
	}
	limit := int(mcp.ParseInt(request, "limit", defaultSchemaSearchLimit))
	if limit < 1 || limit > maxSchemaSearchLimit {
		return mcp.NewToolResultError("limit must be between 1 and 20"), nil
	}
	requestedSchema := strings.TrimSpace(mcp.ParseString(request, "schema", ""))

	dataSource, err := m.datasourceRepo.GetByID(ctx, dataSourceID)
	caller := callerFromContext(ctx)
	if err != nil || dataSource == nil || caller == nil || !caller.CanRead(dataSource) {
		return mcp.NewToolResultError("data source not found or access denied"), nil
	}
	if dataSource.Status != model.DataSourceStatusActive {
		return mcp.NewToolResultError("data source is not active"), nil
	}
	if m.tableDescriber == nil {
		return mcp.NewToolResultError("schema search is not available on this server"), nil
	}
	lister, ok := m.tableDescriber.(TableLister)
	if !ok {
		return mcp.NewToolResultError("schema search is not supported for this server configuration"), nil
	}

	identities, err := lister.ListTables(ctx, dataSourceID)
	if err != nil {
		if errors.Is(err, metadata.ErrSchemaDiscoveryUnsupported) {
			return mcp.NewToolResultError(fmt.Sprintf("schema search is not supported for data source type %s", dataSource.Type)), nil
		}
		return mcp.NewToolResultError("schema metadata is unavailable or access is denied"), nil
	}
	candidates, err := m.permittedSchemaSearchCandidates(ctx, caller, dataSource, identities)
	if err != nil {
		markMCPAuthorizationError(ctx, err)
		if errors.Is(err, security.ErrTablePolicyUnavailable) {
			return mcp.NewToolResultError("table policy is stale; retry after replica synchronization"), nil
		}
		return mcp.NewToolResultError("schema metadata is unavailable or access is denied"), nil
	}
	if requestedSchema != "" {
		filtered := candidates[:0]
		for _, candidate := range candidates {
			if strings.EqualFold(candidate.reference.Schema, requestedSchema) {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}

	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i].reference, candidates[j].reference
		if left.Catalog != right.Catalog {
			return left.Catalog < right.Catalog
		}
		if left.Schema != right.Schema {
			return left.Schema < right.Schema
		}
		if left.Table != right.Table {
			return left.Table < right.Table
		}
		return candidates[i].identity.Type < candidates[j].identity.Type
	})

	truncated := len(candidates) > maxSchemaSearchScan
	if truncated {
		candidates = candidates[:maxSchemaSearchScan]
	}
	response := schemaSearchResponse{
		DataSourceID: dataSourceID,
		Query:        query,
		Matches:      make([]schemaSearchMatch, 0, min(limit, len(candidates))),
		Truncated:    truncated,
	}
	queryFolded := strings.ToLower(query)
	for _, candidate := range candidates {
		match := schemaSearchMatch{
			Catalog: candidate.reference.Catalog,
			Schema:  candidate.reference.Schema,
			Table:   candidate.reference.Table,
			Type:    candidate.identity.Type,
		}
		if fields := matchingTableIdentityFields(candidate.reference, queryFolded); len(fields) > 0 {
			match.MatchedFields = fields
		} else {
			table, describeErr := describeSchemaSearchCandidate(ctx, m.tableDescriber, candidate)
			if describeErr != nil {
				if errors.Is(describeErr, metadata.ErrSchemaDiscoveryUnsupported) {
					return mcp.NewToolResultError(fmt.Sprintf("column metadata search is not supported for data source type %s", dataSource.Type)), nil
				}
				if errors.Is(describeErr, metadata.ErrTableNotFound) {
					return mcp.NewToolResultError("schema changed during search; refresh the table list and retry"), nil
				}
				return mcp.NewToolResultError("schema metadata is unavailable; check the data source and retry"), nil
			}
			if table == nil {
				continue
			}
			visibleColumns := make(map[string]struct{}, len(table.Columns))
			for _, column := range table.Columns {
				visibleColumns[column.Name] = struct{}{}
			}
			if m.tableAuthorizer != nil && m.tableAuthorizer.Enabled() {
				names := make([]string, 0, len(table.Columns))
				for _, column := range table.Columns {
					names = append(names, column.Name)
				}
				allowed, _, filterErr := m.tableAuthorizer.FilterColumns(
					ctx, caller, dataSource, candidate.reference, names, security.TablePermissionMetadata,
				)
				if filterErr != nil {
					markMCPAuthorizationError(ctx, filterErr)
					if errors.Is(filterErr, security.ErrTablePolicyUnavailable) {
						return mcp.NewToolResultError("table policy is stale; retry after replica synchronization"), nil
					}
					return mcp.NewToolResultError("schema metadata is unavailable or access is denied"), nil
				}
				visibleColumns = make(map[string]struct{}, len(allowed))
				for _, name := range allowed {
					visibleColumns[name] = struct{}{}
				}
			}
			match.MatchedFields, match.Columns, truncated = matchingVisibleColumns(table.Columns, visibleColumns, queryFolded, truncated)
			if len(match.MatchedFields) == 0 {
				continue
			}
		}

		if len(response.Matches) == limit {
			response.Truncated = true
			break
		}
		response.Matches = append(response.Matches, match)
	}
	response.Truncated = response.Truncated || truncated
	response.Ambiguous = len(response.Matches) > 1 || response.Truncated

	body, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema search response: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(body))}}, nil
}

func (m *MCPServer) permittedSchemaSearchCandidates(
	ctx context.Context,
	caller *security.Caller,
	dataSource *model.DataSource,
	identities []metadata.TableIdentity,
) ([]schemaSearchCandidate, error) {
	candidates := make([]schemaSearchCandidate, 0, len(identities))
	references := make([]sqlanalysis.TableReference, 0, len(identities))
	for _, identity := range identities {
		catalog := identity.Catalog
		if catalog == "" && isPrestoFamily(dataSource.Type) {
			catalog = dataSource.Config.Database
		}
		references = append(references, sqlanalysis.TableReference{
			Catalog: catalog, Schema: identity.Schema, Table: identity.Name, Unqualified: identity.Schema == "",
		})
	}

	allowedByKey := make(map[string]sqlanalysis.TableReference, len(references))
	if m.tableAuthorizer != nil && m.tableAuthorizer.Enabled() {
		allowed, err := m.tableAuthorizer.FilterReferences(ctx, caller, dataSource, references, security.TablePermissionMetadata)
		if err != nil {
			return nil, err
		}
		for _, reference := range allowed {
			allowedByKey[searchTableReferenceKey(reference)] = reference
		}
	}

	for _, identity := range identities {
		reference := normalizedSchemaSearchReference(dataSource, identity)
		if m.tableAuthorizer != nil && m.tableAuthorizer.Enabled() {
			allowed, ok := allowedByKey[searchTableReferenceKey(reference)]
			if !ok {
				continue
			}
			reference = allowed
		}
		candidates = append(candidates, schemaSearchCandidate{sourceID: dataSource.ID, identity: identity, reference: reference})
	}
	return candidates, nil
}

func normalizedSchemaSearchReference(dataSource *model.DataSource, identity metadata.TableIdentity) sqlanalysis.TableReference {
	reference := sqlanalysis.TableReference{
		Catalog: identity.Catalog, Schema: identity.Schema, Table: identity.Name, Unqualified: identity.Schema == "",
	}
	if dataSource == nil {
		return reference
	}
	if reference.Catalog == "" && isPrestoFamily(dataSource.Type) {
		reference.Catalog = dataSource.Config.Database
	}
	csvObjectSource := dataSource.Type == model.DatabaseTypeMinIOCSV ||
		dataSource.Type == model.DatabaseTypeS3CSV || dataSource.Type == model.DatabaseTypeGCSCSV
	if reference.Schema == "" && !csvObjectSource {
		reference.Schema = dataSource.Config.Schema
		if reference.Schema == "" {
			switch dataSource.Type {
			case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeTiDB,
				model.DatabaseTypeOceanBaseMySQL, model.DatabaseTypeTDSQL, model.DatabaseTypeGaussDBMySQL,
				model.DatabaseTypeGBase8s, model.DatabaseTypeGBase8t:
				reference.Schema = dataSource.Config.Database
			}
		}
	}
	if reference.Schema != "" {
		reference.Unqualified = false
	}
	return reference
}

func searchTableReferenceKey(reference sqlanalysis.TableReference) string {
	return reference.Catalog + "\x00" + reference.Schema + "\x00" + reference.Table
}

func describeSchemaSearchCandidate(
	ctx context.Context,
	describer TableDescriber,
	candidate schemaSearchCandidate,
) (*metadata.TableSchema, error) {
	if candidate.reference.Schema != "" {
		if qualified, ok := describer.(SchemaQualifiedTableDescriber); ok {
			return qualified.ExtractTableSchemaInSchema(ctx, candidate.sourceID, candidate.reference.Schema, candidate.reference.Table)
		}
		if candidate.identity.Schema != "" {
			return nil, metadata.ErrSchemaDiscoveryUnsupported
		}
	}
	return describer.ExtractTableSchema(ctx, candidate.sourceID, candidate.reference.Table)
}

func matchingTableIdentityFields(reference sqlanalysis.TableReference, query string) []string {
	var fields []string
	if strings.Contains(strings.ToLower(reference.Catalog), query) {
		fields = append(fields, "catalog_name")
	}
	if strings.Contains(strings.ToLower(reference.Schema), query) {
		fields = append(fields, "schema_name")
	}
	if strings.Contains(strings.ToLower(reference.Table), query) {
		fields = append(fields, "table_name")
	}
	return fields
}

func matchingVisibleColumns(
	columns []metadata.ColumnSchema,
	visible map[string]struct{},
	query string,
	truncated bool,
) ([]string, []string, bool) {
	var fields []string
	matchedSet := make(map[string]struct{})
	for _, column := range columns {
		if _, ok := visible[column.Name]; !ok {
			continue
		}
		if strings.Contains(strings.ToLower(column.Name), query) {
			fields = append(fields, "column_name")
			matchedSet[column.Name] = struct{}{}
		}
		if strings.Contains(strings.ToLower(column.Comment), query) {
			fields = append(fields, "column_comment")
			matchedSet[column.Name] = struct{}{}
		}
	}
	if len(fields) == 0 {
		return nil, nil, truncated
	}
	sort.Strings(fields)
	fields = compactSchemaSearchStrings(fields)
	matched := make([]string, 0, len(matchedSet))
	for name := range matchedSet {
		matched = append(matched, name)
	}
	sort.Strings(matched)
	if len(matched) > maxSchemaSearchColumns {
		matched = matched[:maxSchemaSearchColumns]
		truncated = true
	}
	return fields, matched, truncated
}

func compactSchemaSearchStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
