package security

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

var (
	ErrTableAccessDenied = errors.New("table access denied")
	// ErrTableAuthorizationUnavailable means the configured policy cannot be
	// enforced because dialect-aware AST analysis is unavailable.
	ErrTableAuthorizationUnavailable = errors.New("table authorization requires SQL AST analysis")
)

type TablePermission string

const (
	TablePermissionRead     TablePermission = "read"
	TablePermissionMetadata TablePermission = "metadata"
)

// TableAuthorizationConfig defines static, per-data-source table policies.
// An enabled policy is deny-by-default: an absent source policy or unmatched
// rule grants no table access.
type TableAuthorizationConfig struct {
	Enabled     bool                             `mapstructure:"enabled" json:"enabled"`
	DataSources map[string]DataSourceTablePolicy `mapstructure:"data_sources" json:"data_sources,omitempty"`
}

type DataSourceTablePolicy struct {
	AdminOverride bool              `mapstructure:"admin_override" json:"admin_override,omitempty"`
	Rules         []TableAccessRule `mapstructure:"rules" json:"rules,omitempty"`
}

type TableAccessRule struct {
	UserID   string       `mapstructure:"user_id" json:"user_id,omitempty"`
	TenantID string       `mapstructure:"tenant_id" json:"tenant_id,omitempty"`
	Read     []TableScope `mapstructure:"read" json:"read,omitempty"`
	Metadata []TableScope `mapstructure:"metadata" json:"metadata,omitempty"`
}

// TableScope names one table, or an explicit wildcard scope. Empty catalog or
// schema means the reference must also be unqualified at that level.
type TableScope struct {
	Catalog string `mapstructure:"catalog" json:"catalog,omitempty"`
	Schema  string `mapstructure:"schema" json:"schema,omitempty"`
	Table   string `mapstructure:"table" json:"table"`
	// Columns is an optional exact-name allowlist. Omitted means all columns;
	// an explicit empty list denies every column in the matched table scope.
	Columns []string `mapstructure:"columns" json:"columns,omitempty"`
}

// Validate checks policy structure before the server opens its metadata DB.
func (c TableAuthorizationConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	for sourceID, policy := range c.DataSources {
		if strings.TrimSpace(sourceID) == "" {
			return fmt.Errorf("table_authorization.data_sources contains an empty data-source ID")
		}
		for ruleIndex, rule := range policy.Rules {
			if strings.TrimSpace(rule.UserID) == "" && strings.TrimSpace(rule.TenantID) == "" {
				return fmt.Errorf("table_authorization.data_sources.%s.rules[%d] requires user_id or tenant_id", sourceID, ruleIndex)
			}
			for _, permission := range []struct {
				name   string
				scopes []TableScope
			}{{"read", rule.Read}, {"metadata", rule.Metadata}} {
				for scopeIndex, scope := range permission.scopes {
					if strings.TrimSpace(scope.Table) == "" {
						return fmt.Errorf("table_authorization.data_sources.%s.rules[%d].%s[%d].table is required", sourceID, ruleIndex, permission.name, scopeIndex)
					}
					for columnIndex, column := range scope.Columns {
						if strings.TrimSpace(column) == "" || strings.TrimSpace(column) == "*" {
							return fmt.Errorf("table_authorization.data_sources.%s.rules[%d].%s[%d].columns[%d] must be a specific column name", sourceID, ruleIndex, permission.name, scopeIndex, columnIndex)
						}
					}
				}
			}
		}
	}
	return nil
}

type TableAuthorizer struct {
	config TableAuthorizationConfig
}

func NewTableAuthorizer(config TableAuthorizationConfig) *TableAuthorizer {
	return &TableAuthorizer{config: config}
}

func (a *TableAuthorizer) Enabled() bool {
	return a != nil && a.config.Enabled
}

// AuthorizeAnalysis checks every physical table and column found by the SQL AST
// parser. Unresolved sources fail closed whenever table authorization is enabled.
func (a *TableAuthorizer) AuthorizeAnalysis(ctx context.Context, caller *Caller, dataSource *model.DataSource, analysis sqlanalysis.Analysis, permission TablePermission) error {
	if !a.Enabled() {
		return nil
	}
	if caller == nil || dataSource == nil || analysis.UnresolvedTables {
		return ErrTableAccessDenied
	}
	if err := a.AuthorizeReferences(ctx, caller, dataSource, analysis.Tables, permission); err != nil {
		return err
	}
	if len(analysis.Tables) == 0 {
		if len(analysis.Columns) == 0 && !analysis.UnresolvedColumns {
			return nil
		}
		return ErrTableAccessDenied
	}
	policy, exists := a.config.DataSources[dataSource.ID]
	if !exists {
		return ErrTableAccessDenied
	}
	if caller != nil && caller.IsAdmin() && policy.AdminOverride {
		return nil
	}

	for _, column := range analysis.Columns {
		table := qualifyTableReference(dataSource, sqlanalysis.TableReference{
			Catalog: column.Catalog, Schema: column.Schema, Table: column.Table, Unqualified: column.Unqualified,
		})
		granted, restricted, allowed := columnGrant(caller, policy.Rules, table, permission, column.Column)
		if !granted || restricted && !allowed {
			slog.WarnContext(ctx, "column authorization denied",
				"data_source_id", dataSource.ID,
				"user_id", caller.UserID,
				"tenant_id", caller.TenantID,
				"permission", permission,
				"table", auditTableNames([]sqlanalysis.TableReference{table}),
				"column", column.Column,
			)
			return ErrTableAccessDenied
		}
	}

	if analysis.UnresolvedColumns {
		for _, reference := range analysis.Tables {
			table := qualifyTableReference(dataSource, reference)
			if hasRestrictedColumns(caller, policy.Rules, table, permission) {
				slog.WarnContext(ctx, "column authorization denied for unresolved column reference",
					"data_source_id", dataSource.ID,
					"user_id", caller.UserID,
					"tenant_id", caller.TenantID,
					"permission", permission,
					"table", auditTableNames([]sqlanalysis.TableReference{table}),
				)
				return ErrTableAccessDenied
			}
		}
	}
	return nil
}

func (a *TableAuthorizer) AuthorizeReferences(ctx context.Context, caller *Caller, dataSource *model.DataSource, tables []sqlanalysis.TableReference, permission TablePermission) error {
	if !a.Enabled() || len(tables) == 0 {
		return nil
	}
	allowed, err := a.FilterReferences(ctx, caller, dataSource, tables, permission)
	if err != nil || len(allowed) != len(tables) {
		return ErrTableAccessDenied
	}
	return nil
}

// FilterColumns returns the visible subset of metadata column names. The
// boolean reports whether a restrictive column allowlist applies to this table.
func (a *TableAuthorizer) FilterColumns(ctx context.Context, caller *Caller, dataSource *model.DataSource, table sqlanalysis.TableReference, names []string, permission TablePermission) ([]string, bool, error) {
	if !a.Enabled() {
		return append([]string(nil), names...), false, nil
	}
	if caller == nil || dataSource == nil {
		return nil, false, ErrTableAccessDenied
	}
	policy, exists := a.config.DataSources[dataSource.ID]
	if !exists {
		return nil, false, ErrTableAccessDenied
	}
	if caller.IsAdmin() && policy.AdminOverride {
		return append([]string(nil), names...), false, nil
	}
	table = qualifyTableReference(dataSource, table)
	if !hasTableGrant(caller, policy.Rules, table, permission) {
		return nil, false, ErrTableAccessDenied
	}
	_, restricted, _ := columnGrant(caller, policy.Rules, table, permission, "")
	if !restricted {
		return append([]string(nil), names...), false, nil
	}
	visible := make([]string, 0, len(names))
	for _, name := range names {
		_, _, allowed := columnGrant(caller, policy.Rules, table, permission, name)
		if allowed {
			visible = append(visible, name)
		}
	}
	if filteredCount := len(names) - len(visible); filteredCount > 0 {
		slog.InfoContext(ctx, "column metadata filtered",
			"data_source_id", dataSource.ID,
			"user_id", caller.UserID,
			"tenant_id", caller.TenantID,
			"permission", permission,
			"table", auditTableNames([]sqlanalysis.TableReference{table}),
			"visible_column_count", len(visible),
			"filtered_column_count", filteredCount,
		)
	}
	return visible, true, nil
}

// FilterReferences returns only table references whose subject has the
// requested permission. It is used by metadata listing so hidden names never
// enter an MCP response.
func (a *TableAuthorizer) FilterReferences(ctx context.Context, caller *Caller, dataSource *model.DataSource, tables []sqlanalysis.TableReference, permission TablePermission) ([]sqlanalysis.TableReference, error) {
	if !a.Enabled() {
		return append([]sqlanalysis.TableReference(nil), tables...), nil
	}
	if len(tables) == 0 {
		return []sqlanalysis.TableReference{}, nil
	}
	if caller == nil || dataSource == nil {
		return []sqlanalysis.TableReference{}, nil
	}

	policy, exists := a.config.DataSources[dataSource.ID]
	if !exists {
		return []sqlanalysis.TableReference{}, nil
	}
	if caller.IsAdmin() && policy.AdminOverride {
		slog.WarnContext(ctx, "table authorization admin override",
			"data_source_id", dataSource.ID,
			"user_id", caller.UserID,
			"tenant_id", caller.TenantID,
			"permission", permission,
			"tables", auditTableNames(tables),
			"table_count", len(tables),
		)
		return append([]sqlanalysis.TableReference(nil), tables...), nil
	}

	filtered := make([]sqlanalysis.TableReference, 0, len(tables))
	for _, table := range tables {
		table = qualifyTableReference(dataSource, table)
		if hasTableGrant(caller, policy.Rules, table, permission) {
			filtered = append(filtered, table)
		}
	}
	return filtered, nil
}

func hasTableGrant(caller *Caller, rules []TableAccessRule, table sqlanalysis.TableReference, permission TablePermission) bool {
	for _, rule := range rules {
		if rule.UserID != "" && rule.UserID != caller.UserID {
			continue
		}
		if rule.TenantID != "" && rule.TenantID != caller.TenantID {
			continue
		}

		var scopes []TableScope
		switch permission {
		case TablePermissionRead:
			scopes = rule.Read
		case TablePermissionMetadata:
			scopes = rule.Metadata
		default:
			continue
		}
		for _, scope := range scopes {
			if tableScopeMatches(scope, table) {
				return true
			}
		}
	}
	return false
}

// columnGrant combines matching table scopes using additive grant semantics.
// Any matching scope with Columns omitted grants all columns; otherwise the
// caller receives the union of explicitly named columns.
func columnGrant(caller *Caller, rules []TableAccessRule, table sqlanalysis.TableReference, permission TablePermission, column string) (granted, restricted, allowed bool) {
	if caller == nil {
		return false, false, false
	}
	columnAllowed := false
	for _, rule := range rules {
		if rule.UserID != "" && rule.UserID != caller.UserID {
			continue
		}
		if rule.TenantID != "" && rule.TenantID != caller.TenantID {
			continue
		}
		var scopes []TableScope
		switch permission {
		case TablePermissionRead:
			scopes = rule.Read
		case TablePermissionMetadata:
			scopes = rule.Metadata
		default:
			continue
		}
		for _, scope := range scopes {
			if !tableScopeMatches(scope, table) {
				continue
			}
			granted = true
			if scope.Columns == nil {
				return true, false, true
			}
			restricted = true
			for _, name := range scope.Columns {
				if column != "" && name == column {
					columnAllowed = true
				}
			}
		}
	}
	if column == "" {
		return granted, restricted, false
	}
	return granted, restricted, columnAllowed
}

func hasRestrictedColumns(caller *Caller, rules []TableAccessRule, table sqlanalysis.TableReference, permission TablePermission) bool {
	_, restricted, _ := columnGrant(caller, rules, table, permission, "")
	return restricted
}

// RequiresColumnAuthorization reports whether any referenced table has an
// effective column allowlist for this caller. It lets paginated sessions retain
// table-only behavior while rechecking saved SQL when column policy is active.
func (a *TableAuthorizer) RequiresColumnAuthorization(caller *Caller, dataSource *model.DataSource, tables []sqlanalysis.TableReference, permission TablePermission) bool {
	if !a.Enabled() || caller == nil || dataSource == nil {
		return false
	}
	policy, exists := a.config.DataSources[dataSource.ID]
	if !exists || caller.IsAdmin() && policy.AdminOverride {
		return false
	}
	if len(tables) == 0 {
		for _, rule := range policy.Rules {
			if rule.UserID != "" && rule.UserID != caller.UserID || rule.TenantID != "" && rule.TenantID != caller.TenantID {
				continue
			}
			var scopes []TableScope
			switch permission {
			case TablePermissionRead:
				scopes = rule.Read
			case TablePermissionMetadata:
				scopes = rule.Metadata
			}
			for _, scope := range scopes {
				if scope.Columns != nil {
					return true
				}
			}
		}
	}
	for _, reference := range tables {
		if hasRestrictedColumns(caller, policy.Rules, qualifyTableReference(dataSource, reference), permission) {
			return true
		}
	}
	return false
}

func tableScopeMatches(scope TableScope, table sqlanalysis.TableReference) bool {
	// An unqualified identifier has no provable schema. Only an explicit
	// schema wildcard can authorize it unless the datasource default resolved it.
	if table.Unqualified && scope.Schema != "*" {
		return false
	}
	return matchesTableScopePart(scope.Catalog, table.Catalog) &&
		matchesTableScopePart(scope.Schema, table.Schema) &&
		matchesTableScopePart(scope.Table, table.Table)
}

func matchesTableScopePart(scope, actual string) bool {
	if scope == "*" {
		return true
	}
	return scope == actual
}

func qualifyTableReference(dataSource *model.DataSource, table sqlanalysis.TableReference) sqlanalysis.TableReference {
	if dataSource == nil {
		return table
	}
	switch dataSource.Type {
	case model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng:
		// The protocol driver sends Config.Database and Config.Schema as the
		// default catalog and schema for every statement.
		if table.Catalog == "" {
			table.Catalog = dataSource.Config.Database
		}
		if table.Schema == "" {
			table.Schema = dataSource.Config.Schema
		}
		if table.Catalog != "" && table.Schema != "" {
			table.Unqualified = false
		}
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeTiDB,
		model.DatabaseTypeOceanBaseMySQL, model.DatabaseTypeTDSQL, model.DatabaseTypeGaussDBMySQL,
		model.DatabaseTypeGBase8s, model.DatabaseTypeGBase8t:
		if table.Schema == "" {
			table.Schema = dataSource.Config.Schema
			if table.Schema == "" {
				table.Schema = dataSource.Config.Database
			}
		}
	case model.DatabaseTypePostgreSQL, model.DatabaseTypeGaussDBPostgres, model.DatabaseTypeKingbaseES,
		model.DatabaseTypeOpenGauss, model.DatabaseTypeHologres:
		if table.Schema == "" {
			table.Schema = dataSource.Config.Schema
		}
	}
	if table.Schema != "" {
		table.Unqualified = false
	}
	return table
}

func auditTableNames(tables []sqlanalysis.TableReference) []string {
	const maxNames = 25
	names := make([]string, 0, min(len(tables), maxNames))
	for _, table := range tables {
		if len(names) == maxNames {
			break
		}
		name := table.Table
		if table.Schema != "" {
			name = table.Schema + "." + name
		}
		if table.Catalog != "" {
			name = table.Catalog + "." + name
		}
		names = append(names, name)
	}
	return names
}
