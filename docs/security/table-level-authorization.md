# Table and Column Authorization

Table and column-level authorization adds optional read and metadata permissions inside an already-authorized data source. Data-source ownership and tenant checks still run first. The feature is disabled by default.

## Configuration

Add `table_authorization` to `configs/config.yaml`. Policies are keyed by the data-source ID and match a caller by `user_id`, `tenant_id`, or both. When both are present, both must match.

~~~yaml
table_authorization:
  enabled: true
  data_sources:
    0a12b345-6789-4abc-8def-0123456789ab:
      admin_override: false
      rules:
        - tenant_id: analytics
          read:
            - catalog: lake
              schema: reporting
              table: daily_summary
              columns: [date, total, region]
            - catalog: lake
              schema: sales
              table: "*"
          metadata:
            - catalog: lake
              schema: reporting
              table: daily_summary
        - user_id: analyst-01
          read:
            - schema: private
              table: customer_rollup
              columns: [customer_id, display_name]
          metadata:
            - schema: private
              table: customer_rollup
              columns: [customer_id, display_name]
~~~

Each scope has `catalog`, `schema`, and `table` components. A component equal to `"*"` matches any value at that level. Omitted components match only an omitted component; use `"*"` when the grant should span catalogs or schemas. Table names and scope values are compared exactly.

A scope may also include `columns`, an exact-name allowlist. Omit `columns` to keep the existing table-wide behavior. When provided, only listed columns can be referenced by SQL or exposed by metadata under that scope. An explicit empty list denies every column. If multiple matching scopes apply, their column grants are combined; a matching scope with `columns` omitted grants all columns. Column names are compared exactly, including case.
Column rules apply to top-level physical columns. Fields inside a JSON or structured column follow the parent column permission and are not independently allowlisted.

`read` and `metadata` are independent permissions:

- `read` is required for SQL validation, query execution, and every pagination or streaming continuation.
- `metadata` is required for MCP `list_tables` and `describe_table`. A metadata grant does not allow reading rows, and a read grant does not reveal table names or columns.
- `columns` inside a `read` scope is checked for every resolved column used by the query, including projections, predicates, joins, grouping, ordering, and expressions. The same read policy is rechecked for query execution and page/stream continuations.
- `columns` inside a `metadata` scope filters `describe_table`; hidden names are also removed from primary-key and index column lists; index names are withheld because they are arbitrary and may embed hidden names. Foreign keys and arbitrary table properties are omitted when column filtering applies because they may reveal hidden column names or remote schema details.

A data source with no policy, or a table that matches no caller rule, is denied while authorization is enabled. A request referencing multiple tables must be authorized for every physical table.

## SQL resolution

Queries are parsed using the target data-source dialect. Physical tables inside joins, nested queries, and CTE definitions are checked; CTE names and derived-table aliases are not treated as separate physical tables. If SQL contains a table-valued function or a source the parser cannot resolve, the request is denied. For a table with a column allowlist, the analyzer also fails closed on `SELECT *` (including `COUNT(*)`), CTE or derived-table column lineage, unqualified columns across multiple sources, `JOIN ... USING`, `NATURAL JOIN`, and any column reference it cannot map to a physical table. Qualify join columns with table aliases and select explicit columns to keep the mapping unambiguous. These restrictions apply only when the resolved table has a column allowlist; table-only scopes retain their prior behavior.

Unqualified table names are resolved from `DataSource.Config.Schema`. Trino, Presto, and OpenLooKeng also use `DataSource.Config.Database` as the default catalog; MySQL-family connections use their configured database as the default schema. If the default cannot be determined, a grant with an explicit schema does not match an unqualified reference. Use a schema wildcard only when the grant is intended to cover any schema.

Table authorization requires the configured SQLGlot AST analyzer. If table authorization is enabled and AST analysis is unavailable, query requests fail closed.

## Metadata behavior

`list_tables` filters table identities before serializing its response. `describe_table` checks metadata permission before reading columns, indexes, or keys, then removes columns outside the configured allowlist. Pass the optional `schema` argument when the same table name exists in multiple schemas. An unqualified ambiguous table name is rejected. Column denials and unresolved column-policy denials are written to structured application logs with caller and table context; raw SQL and result values are not logged by this policy check.

## Versioned policy updates

The first enabled startup seeds revision 1 from `table_authorization` in the shared metadata database. The database head becomes authoritative after that point; YAML is bootstrap configuration and cannot disable or replace an existing managed policy. This uses the metadata database already required by the service and adds no mandatory runtime service. Keep every owner pointed at the same transactional metadata database, not an asynchronously lagging read replica.

Administrators can inspect and preflight policy changes before publishing:

- REST: `GET /internal/table-policy`, `GET /internal/table-policy/versions/{revision}`, `GET /internal/table-policy/publications`, and `POST /internal/table-policy/validate`.
- REST publication: `POST /internal/table-policy/publish` with `expectedRevision` and a complete `policy` object.
- Remote MCP: `get_table_policy`, `validate_table_policy`, `publish_table_policy`, and `list_table_policy_publications`. These tools are registered only while managed table authorization is active.

All endpoints and tools require the administrator role. Publication uses compare-and-swap: a stale `expectedRevision` conflicts instead of overwriting a newer policy. Invalid policies leave the active revision unchanged. Successful publication stores the immutable version, advances the head, and writes the publisher/outcome record in one metadata transaction. If that record cannot be committed, the policy is not activated and the API reports that audit was not recorded. Historical versions are read-only; rollback republishes a prior configuration as a new, higher revision.

Each REST request and MCP tool call pins one immutable policy snapshot. New requests and every query continuation recheck the active policy; a continuation denied after revocation is retired. An already-running request may finish using the snapshot it pinned at its boundary.

Healthy replicas poll every 250 ms. The declared revocation propagation window is at most 3 seconds. Owners expose the dynamic table-policy revision and freshness at `/internal/replica`, separately from the static routing/authentication `policyRevision`. A replica that has not successfully synchronized for 3 seconds fails closed for table queries and table metadata, including continuations. Keep routes closed until all serving owners report the intended fresh revision. The reference two-replica acceptance verifies this contract.

The publication history is transactional with policy activation. General access-audit write failures retain the existing behavior documented in [Access audit events](audit-events.md); this feature does not promise that every access event is durably stored.

The `admin_override` option is off unless explicitly enabled. When enabled, administrator table access is still logged with the caller, data source, permission, and a bounded list of table names.

MCP over stdio uses the trusted local system caller with the administrator role. If `admin_override` is enabled for a source, local stdio requests bypass its table rules. Remote MCP requests use their authenticated caller identity.
