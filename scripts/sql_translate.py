#!/usr/bin/env python3
"""Dialect-aware SQL parsing and translation using SQLGlot."""

import sys
import json
import sqlglot
from sqlglot import exp, transpile

def translate_sql(sql, source_dialect, target_dialect):
    """
    Translate SQL from source dialect to target dialect using sqlglot
    """
    try:
        # Map our dialect names to sqlglot dialects if needed
        sqlglot_source = map_dialect(source_dialect)
        sqlglot_target = map_dialect(target_dialect)

        # Parse and transpile the SQL
        result = transpile(
            sql,
            read=sqlglot_source,
            write=sqlglot_target,
            pretty=True,
            unsupported_level=sqlglot.ErrorLevel.RAISE,
        )

        if len(result) != 1:
            raise ValueError("expected exactly one SQL statement, got " + str(len(result)))
        return result[0]
    except Exception as e:
        # Return error as part of JSON response
        return f"ERROR: {str(e)}"


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
                translated_sql = translate_sql(sql, source_dialect, target_dialect)

                if translated_sql.startswith('ERROR:'):
                    response = {'error': translated_sql}
                else:
                    response = {'translated_sql': translated_sql}

        # Output JSON response
        print(json.dumps(response))

    except json.JSONDecodeError:
        error_response = {'error': 'Invalid JSON input'}
        print(json.dumps(error_response))
    except Exception as e:
        error_response = {'error': f'Unexpected error: {str(e)}'}
        print(json.dumps(error_response))

if __name__ == "__main__":
    main()
