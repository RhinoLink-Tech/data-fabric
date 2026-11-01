#!/usr/bin/env python3
"""Summarize versioned live driver contract outcomes from go test -json."""

import argparse
import json
import os
import sys
from typing import Iterable

CONTRACTS = {
    "TestPostgreSQLDriverContract": (
        "PostgreSQL",
        "DATA_FABRIC_POSTGRES_EXPECTED_VERSION",
        "DATA_FABRIC_POSTGRES_CONTRACT_DSN",
    ),
    "TestOpenGaussDriverContract": (
        "openGauss",
        "DATA_FABRIC_OPENGAUSS_EXPECTED_VERSION",
        "DATA_FABRIC_OPENGAUSS_CONTRACT_HOST",
    ),
    "TestMySQLDriverContract": (
        "MySQL",
        "DATA_FABRIC_MYSQL_EXPECTED_VERSION",
        "DATA_FABRIC_MYSQL_CONTRACT_HOST",
    ),
    "TestMariaDBDriverContract": (
        "MariaDB",
        "DATA_FABRIC_MARIADB_EXPECTED_VERSION",
        "DATA_FABRIC_MARIADB_CONTRACT_HOST",
    ),
    "TestClickHouseDriverContract": (
        "ClickHouse",
        "DATA_FABRIC_CLICKHOUSE_EXPECTED_VERSION",
        "DATA_FABRIC_CLICKHOUSE_CONTRACT_HOST",
    ),
    "TestTiDBDriverContract": (
        "TiDB",
        "DATA_FABRIC_TIDB_EXPECTED_VERSION",
        "DATA_FABRIC_TIDB_CONTRACT_HOST",
    ),
}


def parse_results(lines: Iterable[str]) -> dict[str, dict[str, object]]:
    results: dict[str, dict[str, object]] = {}
    for line in lines:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        test_name = event.get("Test", "")
        root_name = test_name.split("/", 1)[0]
        if root_name not in CONTRACTS:
            continue
        result = results.setdefault(root_name, {"action": None, "output": []})
        if event.get("Action") in {"pass", "skip", "fail"} and "/" not in test_name:
            result["action"] = event["Action"]
        if event.get("Action") == "output":
            result["output"].append(event.get("Output", ""))
    return results


def classify(result: dict[str, object] | None, expected_version: str, endpoint: str) -> tuple[str, bool]:
    if result is None:
        if expected_version and endpoint:
            return "No result", True
        return "Not configured", False

    action = result.get("action")
    output = "".join(result.get("output", []))
    if action == "pass":
        return "Passed", False
    if action == "skip":
        if expected_version and endpoint:
            return "Skipped despite configured baseline", True
        return "Skipped: endpoint not configured", False
    if action == "fail":
        lowered = output.lower()
        if "version" in lowered and any(word in lowered for word in ("want", "expected", "mismatch")):
            return "Version mismatch", True
        if "does not identify" in lowered or "product mismatch" in lowered:
            return "Product/compatibility mismatch", True
        return "Failed", True
    return "No result", bool(expected_version and endpoint)


def render_summary(
    results: dict[str, dict[str, object]],
    environ: dict[str, str],
    require_configured: bool = False,
) -> tuple[str, bool]:
    lines = [
        "### Live connector contract results",
        "",
        "| Connector | Expected server version | Result |",
        "| --- | --- | --- |",
    ]
    has_required_failure = False
    configured = 0
    for test_name, (connector, version_env, endpoint_env) in CONTRACTS.items():
        version = environ.get(version_env, "")
        endpoint = environ.get(endpoint_env, "")
        status, failed = classify(results.get(test_name), version, endpoint)
        if version and endpoint:
            configured += 1
        if require_configured and version and endpoint and status != "Passed":
            failed = True
        has_required_failure = has_required_failure or failed
        lines.append(f"| {connector} | {version or 'not configured'} | {status} |")

    if require_configured and configured != len(CONTRACTS):
        has_required_failure = True
    lines.extend([
        "",
        "A contract passes only for its configured exact server version. A version or product mismatch fails the test; versions absent from the [verified baseline report](https://github.com/RhinoLink-Tech/data-fabric/blob/master/docs/driver-contract-tests.md) remain unverified.",
    ])
    return "\n".join(lines) + "\n", has_required_failure


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("json_file", help="path to the go test -json output")
    parser.add_argument(
        "--require-configured",
        action="store_true",
        help="fail unless every configured CI baseline ran and passed",
    )
    args = parser.parse_args()

    try:
        with open(args.json_file, encoding="utf-8") as source:
            results = parse_results(source)
    except OSError as exc:
        print(f"cannot read Go test result file: {exc}", file=sys.stderr)
        return 2

    summary, failed = render_summary(results, dict(os.environ), args.require_configured)
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        try:
            with open(summary_path, "a", encoding="utf-8") as destination:
                destination.write(summary)
        except OSError as exc:
            print(f"cannot append GitHub step summary: {exc}", file=sys.stderr)
            return 2
    else:
        sys.stdout.write(summary)

    if failed:
        for test_name, result in results.items():
            if result.get("action") == "fail":
                print(f"--- {test_name} failed ---", file=sys.stderr)
                sys.stderr.writelines(result.get("output", []))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
