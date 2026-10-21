"""Regression coverage for the documented SQL v1 translation matrix."""
import json
import pathlib
import subprocess
import sys
import unittest

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent
sys.path.insert(0, str(SCRIPT_DIR))

from sql_translate import analyze_sql, map_dialect, translate_sql


def normalize(sql):
    return " ".join(sql.split())


class SQLTranslationRegressionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.contract = (REPO_ROOT / "SQL_TRANSLATION_FEATURES.md").read_text(encoding="utf-8")

    def test_matrix_translations_to_trino(self):
        quote = chr(39)
        tick = chr(96)
        cases = [
            {
                "name": "projection_filter_order_and_limit",
                "source": "mysql",
                "target": "trino",
                "sql": (
                    f"SELECT {tick}order_id{tick}, {tick}status{tick} "
                    f"FROM {tick}orders{tick} WHERE {tick}status{tick} = {quote}paid{quote} "
                    f"ORDER BY {tick}order_id{tick} LIMIT 10"
                ),
                "expected": (
                    'SELECT "order_id", "status" FROM "orders" '
                    'WHERE "status" = \'paid\' ORDER BY "order_id" NULLS FIRST LIMIT 10'
                ),
                "matrix_rows": [
                    "`SELECT` 投影、列别名、常量和基本表达式",
                    "`WHERE` 过滤与布尔条件",
                    "`ORDER BY`",
                    "`LIMIT` 与 `OFFSET`",
                ],
            },
            {
                "name": "join_group_aggregate_having",
                "source": "mysql",
                "target": "trino",
                "sql": (
                    "SELECT c.customer_id, COUNT(*) AS order_count, SUM(o.amount) AS revenue "
                    "FROM orders o INNER JOIN customers c ON o.customer_id = c.customer_id "
                    f"WHERE o.status = {quote}paid{quote} GROUP BY c.customer_id "
                    "HAVING SUM(o.amount) > 0 ORDER BY revenue DESC"
                ),
                "expected": (
                    "SELECT c.customer_id, COUNT(*) AS order_count, SUM(o.amount) AS revenue "
                    "FROM orders AS o INNER JOIN customers AS c ON o.customer_id = c.customer_id "
                    f"WHERE o.status = {quote}paid{quote} GROUP BY c.customer_id "
                    "HAVING SUM(o.amount) > 0 ORDER BY revenue DESC"
                ),
                "matrix_rows": [
                    "`INNER JOIN`、`LEFT JOIN` 等关系连接",
                    "`GROUP BY`、聚合函数和 `HAVING`",
                    "`ORDER BY`",
                ],
            },
            {
                "name": "limit_offset_order",
                "source": "mysql",
                "target": "trino",
                "sql": "SELECT id FROM orders LIMIT 10 OFFSET 5",
                "expected": "SELECT id FROM orders OFFSET 5 LIMIT 10",
                "matrix_rows": ["`LIMIT` 与 `OFFSET`"],
            },
            {
                "name": "date_literal_and_common_function",
                "source": "mysql",
                "target": "trino",
                "sql": (
                    "SELECT COALESCE(total, 0) AS amount FROM orders "
                    f"WHERE created_at >= DATE {quote}2025-01-01{quote}"
                ),
                "expected": (
                    "SELECT COALESCE(total, 0) AS amount FROM orders "
                    f"WHERE created_at >= CAST({quote}2025-01-01{quote} AS DATE)"
                ),
                "matrix_rows": ["常见标量函数与 DATE/TIMESTAMP 字面量"],
            },
        ]

        for case in cases:
            with self.subTest(case=case["name"]):
                for matrix_row in case["matrix_rows"]:
                    self.assertIn(matrix_row, self.contract)
                actual = translate_sql(case["sql"], case["source"], case["target"])
                self.assertFalse(actual.startswith("ERROR:"), actual)
                self.assertEqual(normalize(case["expected"]), normalize(actual))

    def test_presto_and_openlookeng_translation_alias(self):
        sql = "SELECT id FROM orders LIMIT 10"
        presto = translate_sql(sql, "mysql", "presto")
        openlookeng = translate_sql(sql, "mysql", "openlookeng")

        self.assertEqual("presto", map_dialect("openlookeng"))
        self.assertIn("Presto 与 OpenLooKeng", self.contract)
        self.assertEqual("SELECT id FROM orders LIMIT 10", normalize(presto))
        self.assertEqual(normalize(presto), normalize(openlookeng))

    def test_read_only_ast_analysis_across_dialects(self):
        cases = [
            ("mysql", "SELECT `id` FROM `orders` WHERE id = 1"),
            ("postgresql", "SELECT id::text FROM orders"),
            ("oracle", "SELECT name FROM users WHERE ROWNUM <= 5"),
            ("sqlserver", "SELECT TOP 5 [name] FROM dbo.users"),
            ("trino", "SELECT id FROM lake.sales.orders"),
            ("doris", "SELECT id FROM orders LIMIT 5"),
            ("bigquery", "SELECT id FROM `project.dataset.orders` LIMIT 5"),
        ]
        for dialect, sql in cases:
            with self.subTest(dialect=dialect):
                result = analyze_sql(sql, dialect)
                self.assertNotIn("error", result)
                self.assertEqual(1, result["statement_count"])
                self.assertTrue(result["is_query"])
                self.assertFalse(result["has_mutation"])

    def test_ast_table_references_cover_joins_nested_queries_and_ctes(self):
        sql = (
            "WITH recent_orders AS ("
            " SELECT o.id FROM hive.sales.orders o "
            " JOIN crm.public.customers c ON c.id = o.customer_id"
            ") SELECT r.id FROM recent_orders r "
            "JOIN audit.events e ON e.order_id = r.id "
            "WHERE EXISTS (SELECT 1 FROM security.blocked_ids b WHERE b.id = r.id)"
        )
        result = analyze_sql(sql, "trino")
        self.assertNotIn("error", result, result)
        self.assertFalse(result["unresolved_tables"])
        self.assertEqual(
            [
                {"catalog": "", "schema": "audit", "table": "events", "unqualified": False},
                {"catalog": "", "schema": "security", "table": "blocked_ids", "unqualified": False},
                {"catalog": "crm", "schema": "public", "table": "customers", "unqualified": False},
                {"catalog": "hive", "schema": "sales", "table": "orders", "unqualified": False},
            ],
            result["tables"],
        )

    def test_ast_marks_table_functions_unresolved(self):
        result = analyze_sql("SELECT * FROM UNNEST(ARRAY[1, 2]) AS t(value)", "trino")
        self.assertNotIn("error", result, result)
        self.assertTrue(result["unresolved_tables"])

    def test_read_only_ast_analysis_reports_security_constructs(self):
        cases = [
            (
                "multiple statements",
                "SELECT 1; DELETE FROM users",
                "postgres",
                "statement_count",
                2,
            ),
            (
                "nested modifying CTE",
                "WITH removed AS (DELETE FROM users RETURNING *) SELECT * FROM removed",
                "postgres",
                "has_mutation",
                True,
            ),
            (
                "select into",
                "SELECT * INTO leaked FROM users",
                "postgres",
                "has_select_into",
                True,
            ),
            (
                "row lock",
                "SELECT * FROM users FOR UPDATE",
                "postgres",
                "has_locking",
                True,
            ),
        ]
        for name, sql, dialect, key, expected in cases:
            with self.subTest(case=name):
                result = analyze_sql(sql, dialect)
                self.assertNotIn("error", result, result)
                self.assertEqual(expected, result[key])

        writes = [
            ("UPDATE users SET active = false", "postgres"),
            ("CREATE TABLE archive (id INT)", "postgres"),
            ("TRUNCATE TABLE users", "postgres"),
            ("REPLACE INTO users (id) VALUES (1)", "mysql"),
        ]
        for sql, dialect in writes:
            with self.subTest(write=sql):
                result = analyze_sql(sql, dialect)
                self.assertNotIn("error", result, result)
                self.assertTrue(result["has_mutation"], result)
                self.assertFalse(result["is_query"], result)

        dangerous_functions = [
            ("SELECT pg_read_file('/etc/passwd')", "postgres", "PG_READ_FILE"),
            ("SELECT LOAD_FILE('/etc/passwd')", "mysql", "LOAD_FILE"),
            ("SELECT SLEEP(1)", "mysql", "SLEEP"),
            ("SELECT NEXTVAL('orders_id_seq')", "postgres", "NEXTVAL"),
            ("SELECT xp_cmdshell('dir')", "sqlserver", "XP_CMDSHELL"),
        ]
        for sql, dialect, expected_function in dangerous_functions:
            with self.subTest(function=expected_function):
                result = analyze_sql(sql, dialect)
                self.assertNotIn("error", result, result)
                self.assertIn(expected_function, result["functions"])

    def test_analyzer_reports_invalid_sql_and_maps_compatibility_dialects(self):
        self.assertIn("error", analyze_sql("SELECT FROM", "postgres"))
        self.assertEqual("mysql", map_dialect("tdsql"))
        self.assertEqual("postgres", map_dialect("hologres"))
        self.assertEqual("hive", map_dialect("maxcompute"))

    def test_registered_parser_dialects_accept_basic_select(self):
        dialects = [
            "mysql", "postgres", "oracle", "sqlserver", "trino", "presto",
            "snowflake", "databricks", "redshift", "bigquery", "hive",
            "maxcompute", "clickhouse", "doris", "starrocks", "druid", "spark",
        ]
        for dialect in dialects:
            with self.subTest(dialect=dialect):
                result = analyze_sql("SELECT 1", dialect)
                self.assertNotIn("error", result, result)
                self.assertTrue(result["is_query"], result)

    def test_parse_and_dialect_errors_are_explicit(self):
        cases = [
            ("malformed SQL", "SELECT FROM", "mysql", "trino"),
            ("unknown source dialect", "SELECT 1", "not_a_dialect", "trino"),
            ("unknown target dialect", "SELECT 1", "mysql", "not_a_dialect"),
            ("multiple statements", "SELECT 1; SELECT 2", "mysql", "trino"),
        ]
        for name, sql, source, target in cases:
            with self.subTest(case=name):
                translated = translate_sql(sql, source, target)
                self.assertTrue(translated.startswith("ERROR:"), translated)
                self.assertGreater(len(translated), len("ERROR:"))
                if name == "multiple statements":
                    self.assertIn("exactly one SQL statement", translated)

    def test_unsupported_target_construct_is_not_silently_returned(self):
        # SQLGlot documents this as an unsupported conversion fixture. Hive is
        # used here to exercise the strict failure path, not as a v1 target.
        translated = translate_sql(
            "SELECT APPROX_DISTINCT(a, 0.1) FROM foo",
            "trino",
            "hive",
        )
        self.assertTrue(translated.startswith("ERROR:"), translated)
        self.assertIn("not supported for expression", translated)

    def test_cli_reports_translation_failures_as_json_errors(self):
        request = {
            "sql": "SELECT FROM",
            "source_dialect": "mysql",
            "target_dialect": "trino",
        }
        result = subprocess.run(
            [sys.executable, str(REPO_ROOT / "scripts" / "sql_translate.py")],
            cwd=REPO_ROOT,
            input=json.dumps(request),
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(0, result.returncode, result.stderr)
        response = json.loads(result.stdout)
        self.assertIn("error", response)
        self.assertNotIn("translated_sql", response)

    def test_cli_returns_ast_facts_without_returning_sql(self):
        request = {
            "action": "analyze",
            "sql": "SELECT pg_read_file('/etc/passwd')",
            "dialect": "postgres",
        }
        result = subprocess.run(
            [sys.executable, str(REPO_ROOT / "scripts" / "sql_translate.py")],
            cwd=REPO_ROOT,
            input=json.dumps(request),
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(0, result.returncode, result.stderr)
        response = json.loads(result.stdout)
        self.assertEqual(1, response["statement_count"])
        self.assertTrue(response["is_query"])
        self.assertIn("PG_READ_FILE", response["functions"])
        self.assertNotIn("sql", response)


if __name__ == "__main__":
    unittest.main()
