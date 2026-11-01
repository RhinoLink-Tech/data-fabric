import json
import unittest

from summarize_driver_contracts import parse_results, render_summary


class DriverContractSummaryTests(unittest.TestCase):
    def event(self, action, test, output=""):
        return json.dumps({
            "Action": action,
            "Package": "example/internal/database/drivers",
            "Test": test,
            "Output": output,
        })

    def test_reports_pass_skip_and_version_mismatch(self):
        events = [
            self.event("pass", "TestPostgreSQLDriverContract"),
            self.event("skip", "TestOpenGaussDriverContract"),
            self.event(
                "output",
                "TestMySQLDriverContract",
                '    contract_test.go:42: server version = "8.4", want exact contract version "8.0.46"\n',
            ),
            self.event("fail", "TestMySQLDriverContract"),
        ]
        results = parse_results(events)
        environ = {
            "DATA_FABRIC_POSTGRES_EXPECTED_VERSION": "17.11",
            "DATA_FABRIC_POSTGRES_CONTRACT_DSN": "postgres://fixture",
            "DATA_FABRIC_OPENGAUSS_EXPECTED_VERSION": "6.0.0",
            "DATA_FABRIC_OPENGAUSS_CONTRACT_HOST": "localhost",
            "DATA_FABRIC_MYSQL_EXPECTED_VERSION": "8.0.46",
            "DATA_FABRIC_MYSQL_CONTRACT_HOST": "localhost",
        }

        summary, failed = render_summary(results, environ, require_configured=True)

        self.assertIn("| PostgreSQL | 17.11 | Passed |", summary)
        self.assertIn("| openGauss | 6.0.0 | Skipped despite configured baseline |", summary)
        self.assertIn("| MySQL | 8.0.46 | Version mismatch |", summary)
        self.assertTrue(failed)

    def test_reports_unconfigured_contract_as_skip(self):
        results = parse_results([self.event("skip", "TestTiDBDriverContract")])

        summary, failed = render_summary(results, {})

        self.assertIn("| TiDB | not configured | Skipped: endpoint not configured |", summary)
        self.assertFalse(failed)

    def test_all_configured_baselines_must_pass(self):
        from summarize_driver_contracts import CONTRACTS

        results = {
            test_name: {"action": "pass", "output": []}
            for test_name in CONTRACTS
        }
        environ = {}
        for _, version_env, endpoint_env in CONTRACTS.values():
            environ[version_env] = "pinned-version"
            environ[endpoint_env] = "contract-endpoint"

        summary, failed = render_summary(results, environ, require_configured=True)

        self.assertFalse(failed)
        self.assertEqual(summary.count("| Passed |"), len(CONTRACTS))


if __name__ == "__main__":
    unittest.main()
