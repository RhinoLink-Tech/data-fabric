import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import contract_checks as checks


class SelectionTests(unittest.TestCase):
    def select(self, *paths):
        with patch.object(checks, "git_paths", return_value=list(paths)):
            return checks.select_plan("pull_request", "base", "head")

    def test_documentation_only_explicitly_selects_nothing(self):
        plan = self.select("README_zh.md", "docs/ci-workflow.md", "assets/data-fabric-icon.png")
        self.assertEqual(plan["jobs"], [])
        self.assertEqual(plan["diff_status"], "documentation only")

    def test_shared_and_unknown_code_select_reference(self):
        for path in ("internal/security/policy.go", "internal/mcp/query.go", "new_tool.py", "docs/fixture.py"):
            with self.subTest(path=path):
                self.assertEqual(self.select(path)["jobs"], list(checks.REFERENCE))

    def test_specific_drivers_and_shared_reader_select_applicable_jobs(self):
        cases = (
            ("traditional/postgresql_driver.go", {"postgres-version-contracts", "postgres-tls-contract"}),
            ("traditional/mysql_driver.go", set(checks.MYSQL_FAMILY)),
            ("traditional/oracle_driver.go", {"oracle-contract"}),
            ("traditional/sqlserver_driver.go", {"sqlserver-contract"}),
            ("domestic/tidb_driver.go", {"full-checks", "tidb-tls-contract"}),
            ("domestic/opengauss_driver.go", {"opengauss-tls-contract"}),
            ("olap/clickhouse_driver.go", {"full-checks", "clickhouse-tls-contract"}),
            ("olap/doris_driver.go", {"doris-contract"}),
            ("olap/starrocks_driver.go", {"starrocks-contract"}),
            ("nosql/mongo_driver.go", {"mongodb-contract"}),
            ("file_system/hdfs_client.go", set(checks.HDFS)),
            ("object_storage/avro_reader.go", set(checks.SHARED_OBJECT_STORAGE)),
            ("object_storage/minio_client.go", {"minio-csv-contract"}),
            ("object_storage/s3_client.go", {"s3-csv-contract", "minio-csv-contract"}),
            ("table_formats/iceberg_sql.go", {"minio-csv-contract"}),
            ("object_storage/cos_client.go", set()),
        )
        for path, expected in cases:
            with self.subTest(path=path):
                plan = self.select("internal/database/drivers/" + path)
                self.assertEqual(set(plan["jobs"]), expected | set(checks.REFERENCE))

    def test_unknown_driver_or_global_infrastructure_selects_all(self):
        for path in ("go.sum", "scripts/ci/contract_checks.py", "internal/database/drivers/new_driver.go"):
            with self.subTest(path=path):
                self.assertEqual(set(self.select(path)["jobs"]), set(checks.JOBS))

    def test_missing_and_malformed_diff_conservatively_select_all(self):
        self.assertEqual(set(checks.select_plan("pull_request", "0" * 40, "head")["jobs"]), set(checks.JOBS))
        with patch.object(checks, "git_paths", side_effect=ValueError("bad diff")):
            self.assertEqual(set(checks.select_plan("push", "base", "head")["jobs"]), set(checks.JOBS))
        with self.assertRaises(ValueError):
            checks.parse_name_status(b"R100\0only_old_path\0")

    def test_scheduled_matrix_preserves_old_jobs_without_duplicate_reference_jobs(self):
        with patch.object(checks, "git_paths", side_effect=AssertionError("schedule must not need a diff")):
            for event in ("schedule", "workflow_dispatch"):
                plan = checks.select_plan(event, "", "head")
                self.assertEqual(plan["jobs"], list(checks.SCHEDULED_JOBS))
                self.assertIn("full-checks", plan["jobs"])
                self.assertIn("public-reference-acceptance", plan["jobs"])
                self.assertNotIn("reference-contracts", plan["jobs"])
                self.assertNotIn("postgres-tls-contract", plan["jobs"])

    def test_git_rename_and_delete_preserve_connector_impact(self):
        with tempfile.TemporaryDirectory() as temp:
            repo = Path(temp)

            def git(*args):
                return subprocess.check_output(["git", "-C", temp, *args], stderr=subprocess.DEVNULL).decode().strip()

            git("init", "-q")
            git("config", "user.name", "Contract selector test")
            git("config", "user.email", "selector-test@example.invalid")
            git("config", "commit.gpgsign", "false")
            postgres = "internal/database/drivers/traditional/postgresql_driver.go"
            oracle = "internal/database/drivers/traditional/oracle_driver.go"
            (repo / postgres).parent.mkdir(parents=True)
            (repo / postgres).write_text("PostgreSQL test source\n", encoding="utf-8")
            (repo / oracle).write_text("Oracle test source\n", encoding="utf-8")
            git("add", "--all")
            git("commit", "-qm", "before")
            base = git("rev-parse", "HEAD")
            git("mv", postgres, "README.md")
            git("rm", "-q", oracle)
            git("commit", "-qm", "rename and delete")
            plan = checks.select_plan("pull_request", base, git("rev-parse", "HEAD"), repo)
            self.assertEqual(set(plan["paths"]), {postgres, oracle, "README.md"})
            self.assertEqual(set(plan["jobs"]), {
                *checks.REFERENCE, "postgres-version-contracts", "postgres-tls-contract", "oracle-contract",
            })

    def test_select_output_matches_gate_plan(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / "output"
            summary = Path(temp) / "summary"
            with patch.dict(os.environ, {"GITHUB_OUTPUT": str(output), "GITHUB_STEP_SUMMARY": str(summary)}):
                self.assertEqual(checks.main(["select", "--event", "schedule", "--base", "", "--head", "abc"]), 0)
            records = dict(line.split("=", 1) for line in output.read_text().splitlines())
            self.assertEqual(json.loads(records["selected_jobs"]), json.loads(records["plan"])["jobs"])
            self.assertIn("complete scheduled or manual matrix", summary.read_text())


class GateTests(unittest.TestCase):
    def test_success_requires_selector_fast_checks_and_selected_jobs(self):
        plan = {"version": 1, "jobs": ["reference-contracts"]}
        needs = {job: {"result": "success"} for job in (*checks.REQUIRED_BASE, "reference-contracts")}
        needs["oracle-contract"] = {"result": "skipped"}
        rows, failures = checks.evaluate_gate(plan, needs)
        self.assertEqual(failures, [])
        self.assertIn(("oracle-contract", "Not selected", "Intentionally unselected"), rows)
        for job in (*checks.REQUIRED_BASE, "reference-contracts"):
            for result in (None, {"result": "skipped"}, {"result": "failure"}, {"result": "cancelled"}):
                with self.subTest(job=job, result=result):
                    changed = dict(needs)
                    if result is None:
                        del changed[job]
                    else:
                        changed[job] = result
                    self.assertIn(job, checks.evaluate_gate(plan, changed)[1])

    def test_missing_selector_blocks_even_documentation_only(self):
        _, failures = checks.evaluate_gate({"version": 1, "jobs": []}, {"fast-checks": {"result": "success"}})
        self.assertEqual(failures, ["contract-selection"])

    def test_invalid_plan_never_passes(self):
        for plan in (None, {}, {"version": 2, "jobs": []}, {"version": 1, "jobs": ["unknown"]},
                     {"version": 1, "jobs": ["reference-contracts", "reference-contracts"]}):
            with self.subTest(plan=plan), self.assertRaises(ValueError):
                checks.evaluate_gate(plan, {})


class EvidenceTests(unittest.TestCase):
    def test_root_test_pass_is_required_for_every_contract(self):
        passed = "--- PASS: TestPostgreSQLDriverContract (0.03s)\n"
        statuses = checks.contract_statuses("reference-contracts", [passed, "PASS\n"])
        self.assertEqual(statuses["TestPostgreSQLDriverContract"], "Passed")
        self.assertEqual(statuses["TestMySQLDriverContract"], "Missing result")

    def test_skip_or_failure_is_not_masked_by_pass(self):
        for outcome, expected in (("SKIP", "Unexpectedly skipped"), ("FAIL", "Failed")):
            with self.subTest(outcome=outcome):
                statuses = checks.contract_statuses("oracle-contract", [
                    f"    --- {outcome}: TestOracleDriverContract/child (0.00s)\n",
                    "--- PASS: TestOracleDriverContract (0.01s)\n",
                ])
                self.assertEqual(statuses, {"TestOracleDriverContract": expected})

    def test_fixture_pass_does_not_satisfy_live_contract(self):
        statuses = checks.contract_statuses("minio-csv-contract", [
            "--- PASS: TestMinIOAvroReadOnlySQLContract (0.01s)\n",
            "--- SKIP: TestMinIOAvroContract (0.00s)\n",
        ])
        self.assertEqual(statuses["TestMinIOAvroContract"], "Unexpectedly skipped")
        self.assertEqual(statuses["TestMinIOCSVContract"], "Missing result")

    def test_cached_package_pass_is_not_live_evidence(self):
        statuses = checks.contract_statuses("oracle-contract", [
            "--- PASS: TestOracleDriverContract (0.02s)\n",
            "ok  \tgithub.com/example/drivers/traditional\t(cached)\n",
        ])
        self.assertEqual(statuses, {"TestOracleDriverContract": "Cached evidence is not accepted"})

    def test_require_cli_rejects_missing_and_skipped_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            log = Path(temp) / "test.log"
            summary = Path(temp) / "summary.md"
            args = ["require", "--job", "oracle-contract", "--log", str(log)]
            with patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": str(summary)}):
                self.assertEqual(checks.main(args), 1)
                log.write_text("--- SKIP: TestOracleDriverContract (0.00s)\nPASS\n", encoding="utf-8")
                self.assertEqual(checks.main(args), 1)
                log.write_text("--- PASS: TestOracleDriverContract (0.02s)\nPASS\n", encoding="utf-8")
                self.assertEqual(checks.main(args), 0)
            self.assertIn("Unavailable", summary.read_text())
            self.assertIn("Unexpectedly skipped", summary.read_text())


if __name__ == "__main__":
    unittest.main()
