#!/usr/bin/env python3
"""Select bounded live CI contracts and reject missing required evidence."""

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys


JOBS = {
    "postgres-version-contracts": ("TestPostgreSQLDriverContract",),
    "mysql-lts-contract": ("TestMySQLDriverContract",),
    "mariadb-lts-contract": ("TestMariaDBDriverContract",),
    "mariadb-114-contract": ("TestMariaDBDriverContract",),
    "mariadb-123-contract": ("TestMariaDBDriverContract",),
    "hdfs-csv-contract": (
        "TestHDFSCSVDriverContract", "TestHDFSAvroDriverContract", "TestHDFSTextDriverContract",
    ),
    "hdfs-parquet-contract": ("TestHDFSParquetDriverContract", "TestHDFSORCDriverContract"),
    # The full job retains its existing JSON summary and configured-baseline gate.
    "full-checks": (
        "TestPostgreSQLDriverContract", "TestMySQLDriverContract", "TestMariaDBDriverContract",
        "TestClickHouseDriverContract", "TestTiDBDriverContract", "TestOpenGaussDriverContract",
    ),
    "mysql-tls-contract": ("TestMySQLTLSVerification",),
    "mysql-84-tls-contract": ("TestMySQLTLSVerification",),
    "mariadb-tls-contract": ("TestMariaDBTLSVerification",),
    "mariadb-1011-tls-contract": ("TestMariaDBTLSVerification",),
    "mariadb-114-tls-contract": ("TestMariaDBTLSVerification",),
    "mariadb-123-tls-contract": ("TestMariaDBTLSVerification",),
    "tidb-tls-contract": ("TestTiDBTLSVerification",),
    "opengauss-tls-contract": ("TestOpenGaussTLSVerification", "TestOpenGaussDriverContract"),
    "clickhouse-tls-contract": ("TestClickHouseTLSVerification",),
    "sqlserver-contract": ("TestSQLServerDriverContract", "TestSQLServerTLSCertificateVerification"),
    "mongodb-contract": ("TestMongoDriverContract",),
    "oracle-contract": ("TestOracleDriverContract",),
    "s3-csv-contract": tuple(f"TestS3{fmt}Contract" for fmt in ("CSV", "Parquet", "ORC", "Avro", "JSON", "Delta")),
    "minio-csv-contract": tuple(
        f"TestMinIO{fmt}Contract" for fmt in ("CSV", "Parquet", "JSON", "Avro", "ORC", "Delta", "Iceberg")
    ),
    "doris-contract": ("TestDorisDriverContract", "TestDorisMetadataExtractorContract"),
    "starrocks-contract": ("TestStarRocksDriverContract", "TestStarRocksMetadataExtractorContract"),
    "reference-contracts": ("TestPostgreSQLDriverContract", "TestMySQLDriverContract"),
    # This runner validates the public evidence itself and exits nonzero on a failed case.
    "public-reference-acceptance": (),
    "postgres-tls-contract": ("TestPostgreSQLTLSVerification",),
}
SCHEDULED_JOBS = tuple(job for job in JOBS if job not in {"reference-contracts", "postgres-tls-contract"})
REFERENCE = ("reference-contracts", "public-reference-acceptance")
HDFS = ("hdfs-csv-contract", "hdfs-parquet-contract")
SHARED_OBJECT_STORAGE = ("s3-csv-contract", "minio-csv-contract", *HDFS)
MYSQL_TLS = ("mysql-tls-contract", "mysql-84-tls-contract")
MARIADB_TLS = tuple(job for job in JOBS if job.startswith("mariadb-") and job.endswith("tls-contract"))
MYSQL_FAMILY = (
    "mysql-lts-contract", *MYSQL_TLS,
    "mariadb-lts-contract", "mariadb-114-contract", "mariadb-123-contract", *MARIADB_TLS,
)

# Prefixes are scoped to the connector's own package; other runtime paths
# still select the reference pair and do not imply all providers were tested.
DRIVER_RULES = (
    ("traditional/postgresql_", ("postgres-version-contracts", "postgres-tls-contract"), "PostgreSQL connector"),
    ("traditional/mysql_", MYSQL_FAMILY, "shared MySQL and MariaDB connector"),
    ("traditional/oracle_", ("oracle-contract",), "Oracle connector"),
    ("traditional/sqlserver_", ("sqlserver-contract",), "SQL Server connector"),
    ("domestic/opengauss_", ("opengauss-tls-contract",), "openGauss connector"),
    ("domestic/tidb_", ("full-checks", "tidb-tls-contract"), "TiDB driver and TLS contracts"),
    ("olap/clickhouse_", ("full-checks", "clickhouse-tls-contract"), "ClickHouse driver and TLS contracts"),
    ("olap/doris_", ("doris-contract",), "Doris connector"),
    ("olap/starrocks_", ("starrocks-contract",), "StarRocks connector"),
    ("nosql/mongo_", ("mongodb-contract",), "MongoDB connector"),
    ("file_system/hdfs_", HDFS, "HDFS connector family"),
    ("file_system/common_types.go", HDFS, "shared HDFS code"),
    ("file_system/batch_pagination_test.go", HDFS, "shared HDFS pagination tests"),
    ("object_storage/s3_", ("s3-csv-contract", "minio-csv-contract"), "shared S3-compatible and MinIO code"),
    ("object_storage/minio_", ("minio-csv-contract",), "MinIO connector family"),
    ("table_formats/iceberg_", ("minio-csv-contract",), "shared Iceberg code used by MinIO"),
)
SHARED_OBJECT_PREFIXES = (
    "avro_", "csv_", "delta_snapshot", "json_", "object_sql_adapter", "orc_", "parquet_", "text_sql", "testdata/",
)
NON_LIVE_DRIVER_PREFIXES = (
    "domestic/dameng_", "domestic/gaussdb_", "domestic/gbase_", "domestic/kingbasees_",
    "domestic/oceanbase_", "domestic/oscar_", "domestic/tdsql_", "file_system/ozone_",
    "object_storage/azure_", "object_storage/cos_", "object_storage/oss_", "object_storage/gcs_",
    "object_storage/ozone_", "olap/databricks_", "olap/druid_", "olap/hologres_",
    "table_formats/delta_", "table_formats/hudi_", "warehouses/bigquery_",
    "warehouses/databricks_", "warehouses/hive_", "warehouses/maxcompute_", "warehouses/presto_",
    "warehouses/redshift_", "warehouses/snowflake_", "warehouses/shared_query_engine_driver_test.go",
)
RUNNER_JOBS = {
    "run_clickhouse_tls_contract.sh": ("clickhouse-tls-contract",),
    "run_hdfs_csv_contract.sh": ("hdfs-csv-contract",),
    "run_hdfs_parquet_contract.sh": ("hdfs-parquet-contract",),
    "run_mariadb_tls_contract.sh": MARIADB_TLS,
    "run_minio_object_storage_contract.sh": ("minio-csv-contract",),
    "run_mysql_tls_contract.sh": MYSQL_TLS,
    "run_opengauss_tls_contract.sh": ("opengauss-tls-contract",),
    "run_postgresql_tls_contract.sh": ("postgres-tls-contract", "full-checks"),
    "run_s3_csv_contract.sh": ("s3-csv-contract",),
    "run_sqlserver_tls_contract.sh": ("sqlserver-contract",),
    "run_tidb_tls_contract.sh": ("tidb-tls-contract",),
}
GLOBAL_CI_FILES = {"go.mod", "go.sum", "go.work", "go.work.sum", "Dockerfile", "Makefile"}
GLOBAL_CI_PREFIXES = (".github/workflows/", ".github/actions/", "scripts/ci/", "scripts/validation/")
DOC_SUFFIXES = {".md", ".rst", ".adoc"}
DOC_ASSET_SUFFIXES = {".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".pdf", ".txt", ".csv"}
REQUIRED_BASE = ("contract-selection", "fast-checks")
PLAN_VERSION = 1


def is_documentation(path):
    suffix = PurePosixPath(path).suffix.lower()
    return (
        suffix in DOC_SUFFIXES or path in {"LICENSE", "NOTICE"}
        or path.startswith(("docs/", "assets/")) and suffix in DOC_ASSET_SUFFIXES
    )


def path_contracts(path):
    if (
        path in GLOBAL_CI_FILES or path.startswith(GLOBAL_CI_PREFIXES)
        or re.fullmatch(r"requirements[^/]*\.txt", path)
    ):
        return tuple(JOBS), "shared CI infrastructure or dependencies changed"
    if path.startswith("scripts/contracts/"):
        runner = path.removeprefix("scripts/contracts/")
        if runner in RUNNER_JOBS:
            return (*REFERENCE, *RUNNER_JOBS[runner]), "contract runner changed"
        return tuple(JOBS), "unrecognized contract infrastructure changed"
    if path.startswith("internal/database/metadata/"):
        return (*REFERENCE, "doris-contract", "starrocks-contract"), "shared metadata extraction changed"
    driver_prefix = "internal/database/drivers/"
    if path.startswith(driver_prefix):
        driver = path.removeprefix(driver_prefix)
        for prefix, jobs, label in DRIVER_RULES:
            if driver.startswith(prefix):
                return (*REFERENCE, *jobs), label + " changed"
        if driver.startswith("object_storage/") and driver.removeprefix("object_storage/").startswith(SHARED_OBJECT_PREFIXES):
            return (*REFERENCE, *SHARED_OBJECT_STORAGE), "shared object and HDFS readers changed"
        if driver.startswith(NON_LIVE_DRIVER_PREFIXES):
            return REFERENCE, "connector without a verified live baseline changed; keep its fixture scope"
        if driver in {"drivers.go", "traditional/batch_pagination_test.go"} or driver.startswith("common/"):
            return REFERENCE, "shared driver interface, registration or tests changed"
        return tuple(JOBS), "unrecognized driver path changed"
    return REFERENCE, "shared or unclassified runtime path changed"


def parse_name_status(data):
    """Read git diff --name-status -z and retain both paths of a rename/copy."""
    if not data:
        return []
    if not data.endswith(b"\0"):
        raise ValueError("incomplete name-status record")
    fields = data[:-1].decode("utf-8").split("\0")
    paths = set()
    index = 0
    while index < len(fields):
        status = fields[index]
        index += 1
        if not re.fullmatch(r"[ADMTUXB]|[RC][0-9]{1,3}", status):
            raise ValueError("unexpected name-status record")
        count = 2 if status[0] in "RC" else 1
        record = fields[index:index + count]
        if len(record) != count or any(not path for path in record):
            raise ValueError("missing path in name-status record")
        paths.update(record)
        index += count
    return sorted(paths)


def git_paths(base, head, cwd=None, *, compare_merge_base=True):
    def commit(ref):
        if not ref or set(ref) == {"0"}:
            raise ValueError("missing commit")
        return subprocess.check_output(
            ["git", "rev-parse", "--verify", "--end-of-options", ref + "^{commit}"],
            cwd=cwd, stderr=subprocess.DEVNULL, timeout=30,
        ).decode("ascii").strip()

    base_sha, head_sha = commit(base), commit(head)
    separator = "..." if compare_merge_base else ".."
    return parse_name_status(subprocess.check_output(
        ["git", "diff", "--no-ext-diff", "--name-status", "-z", "--find-renames", "--find-copies",
         f"{base_sha}{separator}{head_sha}", "--"],
        cwd=cwd, stderr=subprocess.DEVNULL, timeout=30,
    ))


def select_plan(event, base, head, cwd=None):
    paths = []
    reasons = {}
    diff_status = "not used"

    def add(jobs, reason):
        for job in jobs:
            reasons.setdefault(job, set()).add(reason)

    if event in {"schedule", "workflow_dispatch"}:
        add(SCHEDULED_JOBS, "complete scheduled or manual matrix")
    elif event in {"pull_request", "push"}:
        try:
            paths = git_paths(base, head, cwd, compare_merge_base=event == "pull_request")
        except (OSError, ValueError, subprocess.SubprocessError):
            diff_status = "unavailable; conservative full selection"
            add(JOBS, "changed paths could not be established")
        else:
            for path in paths:
                if not is_documentation(path):
                    add(*path_contracts(path))
            diff_status = "runtime or CI changes" if reasons else ("documentation only" if paths else "no changed paths")
    else:
        diff_status = "unsupported event; conservative full selection"
        add(JOBS, "event not covered by changed-path selection")
    return {
        "version": PLAN_VERSION,
        "event": event,
        "base": base,
        "head": head,
        "jobs": [job for job in JOBS if job in reasons],
        "reasons": {job: sorted(reasons[job]) for job in JOBS if job in reasons},
        "paths": paths,
        "diff_status": diff_status,
    }


def compact(value):
    return json.dumps(value, separators=(",", ":"))


def summary(markdown):
    destination = os.environ.get("GITHUB_STEP_SUMMARY")
    if destination:
        with open(destination, "a", encoding="utf-8") as stream:
            stream.write(markdown + "\n")
    else:
        print(markdown)


def table(headers, rows):
    def row(values):
        return "| " + " | ".join(str(value).replace("|", "\\|").replace("\r", " ").replace("\n", " ") for value in values) + " |"

    return "\n".join([row(headers), row(["---"] * len(headers)), *(row(values) for values in rows)])


def select_command(args):
    plan = select_plan(args.event, args.base, args.head)
    output = os.environ.get("GITHUB_OUTPUT")
    if output:
        with open(output, "a", encoding="utf-8") as stream:
            stream.write(f"selected_jobs={compact(plan['jobs'])}\nplan={compact(plan)}\n")
    else:
        print(compact(plan))
    rows = [(job, "; ".join(plan["reasons"][job])) for job in plan["jobs"]]
    details = table(("Selected job", "Reason"), rows) if rows else "No live jobs selected intentionally."
    summary(f"### Live contract selection\n\n{plan['diff_status']}; {len(plan['paths'])} changed paths.\n\n{details}\n")
    return 0


def evaluate_gate(plan, needs):
    if not isinstance(plan, dict) or plan.get("version") != PLAN_VERSION:
        raise ValueError("missing or unsupported selection plan")
    selected = plan.get("jobs")
    if (
        not isinstance(selected, list)
        or any(not isinstance(job, str) or job not in JOBS for job in selected)
        or len(set(selected)) != len(selected)
    ):
        raise ValueError("selection plan has invalid, duplicate or unknown jobs")
    if not isinstance(needs, dict):
        raise ValueError("job results are not a JSON object")
    required = set(REQUIRED_BASE) | set(selected)
    rows = []
    failures = []
    for job in (*REQUIRED_BASE, *JOBS):
        result = needs.get(job)
        result = result.get("result") if isinstance(result, dict) else None
        if job not in required:
            if result in {"failure", "cancelled"}:
                failures.append(job)
                rows.append((job, "Not selected", f"Unexpected execution: {result}"))
            else:
                rows.append((job, "Not selected", "Intentionally unselected"))
            continue
        outcomes = {
            "success": "Passed", "failure": "Failed (see job log)", "cancelled": "Unavailable: cancelled",
            "skipped": "Unexpectedly skipped",
        }
        rows.append((job, "Required", outcomes.get(result, "Unavailable: no valid result")))
        if result != "success":
            failures.append(job)
    return rows, failures


def gate_command():
    try:
        plan = json.loads(os.environ.get("CI_CONTRACT_PLAN", ""))
        needs = json.loads(os.environ.get("CI_JOB_RESULTS", ""))
        rows, failures = evaluate_gate(plan, needs)
    except (ValueError, TypeError) as exc:
        summary(f"### Required contract gate\n\nInvalid or unavailable CI evidence: {exc}\n")
        return 1
    conclusion = "Failed: " + ", ".join(failures) if failures else "All required jobs passed."
    summary(f"### Required contract gate\n\n{conclusion}\n\n{table(('Job', 'Selection', 'Result'), rows)}\n")
    return int(bool(failures))


TEST_LINE = re.compile(r"^\s*--- (PASS|SKIP|FAIL): (Test[^\s]+)(?:\s|$)")
CACHED_PACKAGE = re.compile(r"^\s*ok\s+\S+\s+\(cached\)\s*$")


def contract_statuses(job, lines):
    statuses = dict.fromkeys(JOBS[job], "Missing result")
    cached = False
    for line in lines:
        if CACHED_PACKAGE.match(line):
            cached = True
        match = TEST_LINE.match(line)
        if not match:
            continue
        outcome, test = match.groups()
        root, separator, _ = test.partition("/")
        if root not in statuses:
            continue
        if outcome == "FAIL":
            statuses[root] = "Failed"
        elif outcome == "SKIP" and statuses[root] != "Failed":
            statuses[root] = "Unexpectedly skipped"
        elif outcome == "PASS" and not separator and statuses[root] == "Missing result":
            statuses[root] = "Passed"
    if cached:
        return dict.fromkeys(JOBS[job], "Cached evidence is not accepted")
    return statuses


def require_command(args):
    try:
        with Path(args.log).open(encoding="utf-8", errors="replace") as stream:
            statuses = contract_statuses(args.job, stream)
    except OSError:
        statuses = dict.fromkeys(JOBS[args.job], "Unavailable: test log missing or unreadable")
    summary(f"### Live evidence for `{args.job}`\n\n{table(('Required test', 'Result'), statuses.items())}\n")
    return int(any(status != "Passed" for status in statuses.values()))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    select = commands.add_parser("select")
    select.add_argument("--event", required=True)
    select.add_argument("--base", required=True)
    select.add_argument("--head", required=True)
    commands.add_parser("gate")
    require = commands.add_parser("require")
    require.add_argument("--job", choices=JOBS, required=True)
    require.add_argument("--log", required=True)
    args = parser.parse_args(argv)
    if args.command == "select":
        return select_command(args)
    if args.command == "gate":
        return gate_command()
    return require_command(args)


if __name__ == "__main__":
    sys.exit(main())
