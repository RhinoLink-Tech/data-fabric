# Table-Level Authorization

Table-level authorization adds optional read and metadata permissions inside an already-authorized data source. Data-source ownership and tenant checks still run first. The feature is disabled by default.

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
          metadata:
            - schema: private
              table: customer_rollup
~~~

Each scope has `catalog`, `schema`, and `table` components. A component equal to `"*"` matches any value at that level. Omitted components match only an omitted component; use `"*"` when the grant should span catalogs or schemas. Table names and scope values are compared exactly.

`read` and `metadata` are independent permissions:

- `read` is required for SQL validation, query execution, and every pagination or streaming continuation.
- `metadata` is required for MCP `list_tables` and `describe_table`. A metadata grant does not allow reading rows, and a read grant does not reveal table names or columns.

A data source with no policy, or a table that matches no caller rule, is denied while authorization is enabled. A request referencing multiple tables must be authorized for every physical table.

## SQL resolution

Queries are parsed using the target data-source dialect. Physical tables inside joins, nested queries, and CTE definitions are checked; CTE names and derived-table aliases are not treated as separate physical tables. If SQL contains a table-valued function or a source the parser cannot resolve, the request is denied.

Unqualified table names are resolved from `DataSource.Config.Schema`. Trino, Presto, and OpenLooKeng also use `DataSource.Config.Database` as the default catalog; MySQL-family connections use their configured database as the default schema. If the default cannot be determined, a grant with an explicit schema does not match an unqualified reference. Use a schema wildcard only when the grant is intended to cover any schema.

Table authorization requires the configured SQLGlot AST analyzer. If table authorization is enabled and AST analysis is unavailable, query requests fail closed.

## Metadata behavior

`list_tables` filters table identities before serializing its response. `describe_table` checks metadata permission before reading columns, indexes, or keys. Pass the optional `schema` argument when the same table name exists in multiple schemas. An unqualified ambiguous table name is rejected.

Policy updates are read from configuration at server startup, so restart the service after changing them. The `admin_override` option is off unless explicitly enabled. When enabled, administrator access is logged with the caller, data source, permission, and a bounded list of table names.

MCP over stdio uses the trusted local system caller with the administrator role. If `admin_override` is enabled for a source, local stdio requests bypass its table rules. Remote MCP requests use their authenticated caller identity.
