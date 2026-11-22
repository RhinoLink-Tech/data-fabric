#!/usr/bin/env python3
"""Dialect-aware SQL parsing and translation using SQLGlot."""

import sys
import json
import logging
import sqlglot
from sqlglot import Dialect, exp, transpile
from sqlglot.errors import ParseError, TokenError, UnsupportedError
from sqlglot.optimizer.scope import Scope, traverse_scope

class InvalidTranslationInput(ValueError):
    """An invalid dialect or SQL input, as opposed to a runtime failure."""


def translate_sql_checked(sql, source_dialect, target_dialect):
    sqlglot_source = map_dialect(source_dialect)
    sqlglot_target = map_dialect(target_dialect)
    try:
        Dialect.get_or_raise(sqlglot_source)
        Dialect.get_or_raise(sqlglot_target)
    except ValueError as exc:
        raise InvalidTranslationInput("unknown SQL dialect") from exc
    result = transpile(
        sql, read=sqlglot_source, write=sqlglot_target, pretty=True,
        unsupported_level=sqlglot.ErrorLevel.RAISE,
    )
    if len(result) != 1:
        raise InvalidTranslationInput("expected exactly one SQL statement, got " + str(len(result)))
    if not result[0] or not result[0].strip():
        raise InvalidTranslationInput("expected one non-empty SQL statement")
    return result[0]


def translate_sql(sql, source_dialect, target_dialect):
    """Retain the legacy in-process helper's string return contract."""
    try:
        return translate_sql_checked(sql, source_dialect, target_dialect)
    except InvalidTranslationInput as exc:
        return f"ERROR: {str(exc)}"
    except (ParseError, TokenError):
        return "ERROR: invalid SQL syntax"
    except UnsupportedError:
        return "ERROR: conversion not supported for expression"
    except Exception:
        return "ERROR: SQL translation runtime failed"


def analyze_sql(sql, dialect):
    """Return security facts from the parsed AST without returning SQL text."""
    try:
        statements = sqlglot.parse(sql, read=map_dialect(dialect))
        roots = [statement for statement in statements if statement is not None]
        root = roots[0] if len(roots) == 1 else None
        result = {
            "statement_count": len(statements),
            "is_query": isinstance(root, exp.Query),
            "has_mutation": False,
            "has_select_into": False,
            "has_locking": False,
            "functions": [],
            "tables": [],
            "unresolved_tables": False,
            "columns": [],
            "unresolved_columns": False,
        }
        if root is None:
            return result

        mutation_nodes = {
            "alter", "attach", "command", "commit", "copy", "create", "delete",
            "detach", "drop", "execute", "export", "grant", "import", "insert",
            "loaddata", "merge", "replace", "revoke", "rollback", "set",
            "transaction", "truncatetable", "update", "use",
        }
        functions = set()
        for node in root.walk():
            node_name = type(node).__name__.lower()
            if node_name in mutation_nodes:
                result["has_mutation"] = True
            if node_name == "into":
                result["has_select_into"] = True
            if node_name in {"lock", "locking"}:
                result["has_locking"] = True
            if node.args.get("locks"):
                result["has_locking"] = True
            if isinstance(node, exp.Func):
                # Anonymous nodes retain the submitted function name; built-in
                # function node classes expose their canonical SQL name.
                name = node.name if isinstance(node, exp.Anonymous) else node.sql_name()
                if name:
                    functions.add(str(name).upper())

        result["functions"] = sorted(functions)
        tables = set()
        unresolved_tables = False
        for scope in traverse_scope(root):
            for _, (node, source) in scope.selected_sources.items():
                if isinstance(source, Scope):
                    # CTEs and derived tables are resolved by their own scope,
                    # but table-valued functions such as UNNEST remain unsafe.
                    if isinstance(node, (exp.Func, exp.Unnest)):
                        unresolved_tables = True
                    continue
                if not isinstance(source, exp.Table):
                    # Table functions and parser-specific sources are not safe
                    # to authorize from a table identifier alone.
                    unresolved_tables = True
                    continue
                if not isinstance(source.this, exp.Identifier) or not source.name:
                    unresolved_tables = True
                    continue
                catalog = source.catalog or ""
                schema = source.db or ""
                tables.add((catalog, schema, source.name, not catalog and not schema))

        result["tables"] = [
            {"catalog": catalog, "schema": schema, "table": table, "unqualified": unqualified}
            for catalog, schema, table, unqualified in sorted(tables)
        ]
        result["unresolved_tables"] = unresolved_tables

        # Map every column reference in each lexical scope to a physical table.
        # SQLGlot scopes exclude nested scopes from `scope.columns`, which lets
        # aliases resolve without accidentally binding a nested query twice.
        columns = set()
        unresolved_columns = False
        for scope in traverse_scope(root):
            sources = scope.sources
            if next(scope.find_all(exp.Star), None) is not None:
                unresolved_columns = True
            for column in scope.columns:
                if column.is_star:
                    unresolved_columns = True
                    continue

                source = None
                if column.table:
                    source = sources.get(column.table)
                    if source is None:
                        matches = [value for name, value in sources.items() if name.casefold() == column.table.casefold()]
                        if len(matches) == 1:
                            source = matches[0]
                elif len(sources) == 1:
                    source = next(iter(sources.values()))
                else:
                    # An unqualified identifier across multiple sources (or
                    # with no source) cannot be safely assigned to a table.
                    unresolved_columns = True
                    continue

                if not isinstance(source, exp.Table):
                    # CTEs and derived tables need column lineage before they
                    # can be authorized; fail closed until that is available.
                    unresolved_columns = True
                    continue
                if not isinstance(source.this, exp.Identifier) or not source.name or not column.name:
                    unresolved_columns = True
                    continue

                catalog = source.catalog or ""
                schema = source.db or ""
                columns.add((catalog, schema, source.name, not catalog and not schema, column.name))

            # USING and NATURAL JOIN refer to columns without exp.Column nodes.
            # Their semantics depend on source metadata, so reject them when a
            # caller has a column allowlist rather than guessing both sides.
            for join in scope.find_all(exp.Join):
                if join.args.get("using") or str(join.args.get("method", "")).upper() == "NATURAL":
                    unresolved_columns = True

        result["columns"] = [
            {"catalog": catalog, "schema": schema, "table": table, "unqualified": unqualified, "column": column}
            for catalog, schema, table, unqualified, column in sorted(columns)
        ]
        result["unresolved_columns"] = unresolved_columns
        return result
    except Exception as e:
        return {"error": f"ERROR: {str(e)}"}

def map_dialect(dialect):
    """
    Map our dialect names to sqlglot dialect names
    """
    dialect_mapping = {
        'mysql': 'mysql',
        'mariadb': 'mysql',
        'tidb': 'mysql',
        'tdsql': 'mysql',
        'oceanbase_mysql': 'mysql',
        'gaussdb_mysql': 'mysql',
        'gbase_8t': 'mysql',
        'postgres': 'postgres',
        'postgresql': 'postgres',
        'opengauss': 'postgres',
        'kingbasees': 'postgres',
        'gaussdb_postgres': 'postgres',
        'hologres': 'postgres',
        'trino': 'trino',
        'presto': 'presto',
        'openlookeng': 'presto',
        'spark': 'spark',
        'oracle': 'oracle',
        'oceanbase_oracle': 'oracle',
        'dameng': 'oracle',
        'sqlserver': 'tsql',  # sqlglot uses tsql for SQL Server
        'snowflake': 'snowflake',
        'databricks': 'databricks',
        'bigquery': 'bigquery',
        'hive': 'hive',
        # MaxCompute has no dedicated SQLGlot dialect; Hive parsing provides
        # the closest supported grammar for AST safety checks.
        'maxcompute': 'hive',
        'clickhouse': 'clickhouse',
        'doris': 'doris',
        'starrocks': 'starrocks',
        'sqlite': 'sqlite',
        'duckdb': 'duckdb',
        'redshift': 'redshift'
    }

    return dialect_mapping.get(dialect.lower(), dialect.lower())

def main():
    # SQLGlot fallback warnings may echo the submitted SQL, including literals.
    # The gateway captures stderr, but the standalone CLI must not log them either.
    logging.getLogger("sqlglot").disabled = True
    # Read JSON input from stdin
    input_data = sys.stdin.read().strip()

    try:
        request = json.loads(input_data)
        action = request.get('action', 'translate')
        sql = request.get('sql', '')

        if not sql:
            response = {'error': 'Missing SQL in request'}
        elif action == 'analyze':
            dialect = request.get('dialect', '')
            if not dialect:
                response = {'error': 'Missing dialect in request'}
            else:
                response = analyze_sql(sql, dialect)
        else:
            source_dialect = request.get('source_dialect', '')
            target_dialect = request.get('target_dialect', '')
            if not source_dialect:
                response = {'error': 'Missing source dialect in request'}
            elif not target_dialect:
                response = {'error': 'Missing target dialect in request'}
            else:
                try:
                    translated_sql = translate_sql_checked(sql, source_dialect, target_dialect)
                    response = {'translated_sql': translated_sql}
                except (InvalidTranslationInput, ParseError, TokenError, UnsupportedError):
                    response = {'error': 'SQL syntax or conversion is not supported; check the input and dialects', 'error_kind': 'invalid_input'}
                except Exception:
                    response = {'error': 'SQL translation runtime failed', 'error_kind': 'runtime_failure'}

        # Output JSON response
        print(json.dumps(response))

    except json.JSONDecodeError:
        error_response = {'error': 'Invalid JSON input'}
        print(json.dumps(error_response))
    except Exception:
        error_response = {'error': 'SQL translation runtime failed', 'error_kind': 'runtime_failure'}
        print(json.dumps(error_response))

if __name__ == "__main__":
    main()
