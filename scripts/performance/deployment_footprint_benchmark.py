#!/usr/bin/env python3
"""Measure the release image and runtime footprint with disposable Docker resources."""
from __future__ import annotations

import base64
import csv
import hashlib
import hmac
import json
import os
import platform
import secrets
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
MYSQL_IMAGE = os.environ.get("DATA_FABRIC_FOOTPRINT_MYSQL_IMAGE", "mysql@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7")
ROWS = int(os.environ.get("DATA_FABRIC_FOOTPRINT_ROWS", "50000"))
PAYLOAD_BYTES = 256
BATCH_SIZE = 1000
STARTUP_REPEATS = 5
QUERY_REPEATS = 3
IDLE_SETTLE_SECONDS = 5
SOURCE_DB = "footprint_source"
METADATA_DB = "data_fabric_meta"
APP_USER = "data_fabric"
SECRETS: list[str] = []


def cmd(args: list[str], label: str, stdin: str | None = None) -> str:
    result = subprocess.run(args, cwd=ROOT, input=stdin, text=True, capture_output=True, check=False)
    if result.returncode:
        detail = (result.stderr or result.stdout).strip()
        for secret in SECRETS:
            if secret:
                detail = detail.replace(secret, "<redacted>")
        raise RuntimeError(f"{label} failed (exit {result.returncode}): {detail[-1000:]}")
    return result.stdout.strip()


def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def jwt_token(secret: str) -> str:
    now = int(time.time())
    header = b64url(b'{"alg":"HS256","typ":"JWT"}')
    claims = json.dumps({
        "userId": "footprint-benchmark-admin",
        "username": "footprint-benchmark",
        "roles": ["admin"],
        "sub": "footprint-benchmark-admin",
        "iss": "data-fabric",
        "iat": now,
        "nbf": now,
        "exp": now + 3600,
    }, separators=(",", ":")).encode()
    payload = b64url(claims)
    signing = f"{header}.{payload}".encode()
    signature = hmac.new(secret.encode(), signing, hashlib.sha256).digest()
    return f"{header}.{payload}.{b64url(signature)}"


def request_json(url: str, token: str | None = None, body: dict[str, Any] | None = None) -> dict[str, Any]:
    headers = {"Accept": "application/json"}
    data = None
    method = "GET"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if body is not None:
        method = "POST"
        data = json.dumps(body, separators=(",", ":")).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=180) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"HTTP {error.code} for {urllib.parse.urlsplit(url).path}") from None


def docker_rm(name: str) -> None:
    subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)


def wait_mysql(container: str, password: str) -> None:
    limit = time.monotonic() + 180
    while time.monotonic() < limit:
        check = subprocess.run(
            ["docker", "exec", "-e", f"MYSQL_PWD={password}", container, "mysql", "-uroot", "-Nse", "SELECT 1"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False,
        )
        if check.returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError("disposable MySQL did not become ready within 180 seconds")


def setup_source(container: str, password: str, rows: int) -> None:
    digits = " UNION ALL ".join(f"SELECT {n} AS n" for n in range(10))
    sql = f"""
CREATE DATABASE {SOURCE_DB};
GRANT ALL PRIVILEGES ON {SOURCE_DB}.* TO '{APP_USER}'@'%';
USE {SOURCE_DB};
CREATE TABLE footprint_rows (id BIGINT NOT NULL PRIMARY KEY, payload VARCHAR({PAYLOAD_BYTES}) NOT NULL);
INSERT INTO footprint_rows (id, payload)
SELECT n, RPAD(CONCAT('data-fabric-', n), {PAYLOAD_BYTES}, 'x')
FROM (
 SELECT ones.n + tens.n * 10 + hundreds.n * 100 + thousands.n * 1000 +
        ten_thousands.n * 10000 + 1 AS n
 FROM ({digits}) AS ones
 CROSS JOIN ({digits}) AS tens
 CROSS JOIN ({digits}) AS hundreds
 CROSS JOIN ({digits}) AS thousands
 CROSS JOIN ({digits}) AS ten_thousands
) AS seqs
WHERE n <= {rows};
"""
    cmd(["docker", "exec", "-i", "-e", f"MYSQL_PWD={password}", container, "mysql", "-uroot"],
        "preparing synthetic source", stdin=sql)


def proc_memory(container: str) -> dict[str, int]:
    output = cmd(["docker", "exec", container, "sh", "-c",
                  "awk '/^(VmRSS|VmHWM):/ {print $1, $2}' /proc/1/status"], "sampling server RSS")
    values = {key.rstrip(":"): int(kib) * 1024 for key, kib in (line.split() for line in output.splitlines())}
    if "VmRSS" not in values or "VmHWM" not in values:
        raise RuntimeError("could not read Data Fabric PID 1 VmRSS/VmHWM")
    return values


def start_app(network: str, image: str, name: str, env: dict[str, str]) -> tuple[str, float]:
    started = time.monotonic()
    cmd(["docker", "run", "-d", "--name", name, "--network", network, "--publish", "127.0.0.1::8080",
         *[item for key, value in env.items() for item in ("-e", f"{key}={value}")], image],
        "starting Data Fabric container")
    port = cmd(["docker", "inspect", "--format",
                '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}', name],
               "reading published port")
    base = f"http://127.0.0.1:{port}"
    deadline = started + 90
    while time.monotonic() < deadline:
        try:
            if request_json(f"{base}/health").get("status") == "healthy":
                return base, time.monotonic() - started
        except Exception:
            pass
        time.sleep(0.1)
    logs = subprocess.run(["docker", "logs", name], capture_output=True, text=True, check=False)
    details = (logs.stderr + logs.stdout)[-1200:]
    for secret in SECRETS:
        if secret:
            details = details.replace(secret, "<redacted>")
    raise RuntimeError(f"Data Fabric did not become healthy within 90s: {details}")


def register_source(base: str, token: str, password: str) -> str:
    result = request_json(f"{base}/api/v1/datasources", token, {
        "name": "deployment-footprint-source",
        "type": "mysql",
        "config": {
            "host": "mysql", "port": 3306, "database": SOURCE_DB,
            "username": APP_USER, "password": password, "timeout": 30,
            "maxPoolSize": 4,
        },
    })
    source_id = (result.get("data") or {}).get("id")
    if not result.get("success") or not source_id:
        raise RuntimeError("Data Fabric did not return a data-source ID")
    return str(source_id)


def fetch_small(base: str, token: str, source_id: str) -> None:
    result = request_json(f"{base}/api/v1/fetch", token, {
        "dataSourceId": source_id,
        "sql": "SELECT id, payload FROM footprint_rows WHERE id = 1",
        "type": 1, "batch_size": 10, "timeout": 60,
    })
    if not result.get("success") or len((result.get("data") or {}).get("entries") or []) != 1:
        raise RuntimeError("small query did not return the expected row")


def fetch_stream(base: str, token: str, source_id: str, rows: int) -> int:
    result = request_json(f"{base}/api/v1/fetch", token, {
        "dataSourceId": source_id,
        "sql": "SELECT id, payload FROM footprint_rows ORDER BY id",
        "type": 2, "batch_size": BATCH_SIZE, "timeout": 180,
    })
    if not result.get("success"):
        raise RuntimeError("bounded streaming query failed")
    data = result.get("data") or {}
    total = len(data.get("entries") or [])
    next_uri = data.get("nextUri") or data.get("next_uri")
    requests = 1
    while next_uri:
        if requests > rows // BATCH_SIZE + 3:
            raise RuntimeError("stream returned more batches than expected")
        url = urllib.parse.urljoin(base + "/", next_uri)
        data = (request_json(url, token).get("data") or {})
        total += len(data.get("entries") or [])
        next_uri = data.get("nextUri") or data.get("next_uri")
        requests += 1
    if total != rows:
        raise RuntimeError(f"stream returned {total} rows; expected {rows}")
    return requests


def main() -> int:
    if ROWS < 1000 or ROWS > 100000:
        raise RuntimeError("DATA_FABRIC_FOOTPRINT_ROWS must be between 1,000 and 100,000")
    for program in ("docker",):
        if shutil.which(program) is None:
            raise RuntimeError(f"{program} is required")

    run_id = uuid.uuid4().hex[:10]
    network = f"df-footprint-net-{run_id}"
    mysql = f"df-footprint-mysql-{run_id}"
    image = f"data-fabric-footprint:{run_id}"
    root_password = secrets.token_urlsafe(24)
    db_password = secrets.token_urlsafe(24)
    jwt_secret = secrets.token_urlsafe(48)
    vault_key = base64.b64encode(secrets.token_bytes(32)).decode()
    SECRETS.extend((root_password, db_password, jwt_secret, vault_key))
    token = jwt_token(jwt_secret)
    keyring = json.dumps({"footprint-ephemeral": vault_key}, separators=(",", ":"))
    env = {
        "DATABASE_HOST": "mysql", "DATABASE_PORT": "3306",
        "DATABASE_DATABASE": METADATA_DB, "DATABASE_USERNAME": APP_USER,
        "DATABASE_PASSWORD": db_password, "SERVER_HOST": "0.0.0.0",
        "SERVER_PORT": "8080", "SERVER_MODE": "release",
        "SECURITY_JWT_SECRET": jwt_secret, "SECURITY_ENABLE_RATE_LIMIT": "false",
        "DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID": "footprint-ephemeral",
        "DATA_FABRIC_CREDENTIAL_KEYS": keyring,
    }
    containers: list[str] = []
    work = Path(tempfile.mkdtemp(prefix="data-fabric-footprint-"))
    records: list[dict[str, Any]] = []
    counter = 0

    def new_app(label: str) -> str:
        nonlocal counter
        counter += 1
        name = f"df-footprint-app-{run_id}-{counter}-{label}"
        containers.append(name)
        return name

    def startup(name: str) -> tuple[str, float]:
        return start_app(network, image, name, env)

    try:
        print("Building pinned release image...", flush=True)
        cmd(["docker", "build", "--tag", image, "--file", "Dockerfile", "."], "building release image")
        image_info = json.loads(cmd(["docker", "image", "inspect", image], "inspecting release image"))[0]
        image_size = int(image_info["Size"])
        binary = work / "data-fabric"
        export_container = cmd(["docker", "create", image], "creating release binary export container")
        containers.append(export_container)
        cmd(["docker", "cp", f"{export_container}:/app/main", str(binary)], "extracting release binary")
        binary_size = binary.stat().st_size
        docker_rm(export_container)

        print("Starting isolated MySQL and loading synthetic rows...", flush=True)
        cmd(["docker", "network", "create", network], "creating isolated network")
        containers.append(mysql)
        cmd(["docker", "run", "-d", "--name", mysql, "--network", network, "--network-alias", "mysql",
             "-e", f"MYSQL_ROOT_PASSWORD={root_password}", "-e", f"MYSQL_DATABASE={METADATA_DB}",
             "-e", f"MYSQL_USER={APP_USER}", "-e", f"MYSQL_PASSWORD={db_password}", MYSQL_IMAGE],
            "starting disposable MySQL")
        wait_mysql(mysql, root_password)
        setup_source(mysql, root_password, ROWS)
        mysql_version = cmd(["docker", "exec", "-e", f"MYSQL_PWD={root_password}", mysql, "mysql", "-uroot", "-Nse", "SELECT VERSION()"], "reading MySQL version")
        mysql_image_id = cmd(["docker", "inspect", "--format", "{{.Image}}", mysql], "reading MySQL image ID")

        setup_app = new_app("setup")
        setup_url, first_start = startup(setup_app)
        source_id = register_source(setup_url, token, db_password)
        records.append({"metric": "first_start_to_health", "scenario": "fresh_metadata", "repeat": 1,
                        "value": first_start, "unit": "seconds", "notes": "Includes initial metadata migrations."})
        docker_rm(setup_app)

        start_samples: list[float] = []
        idle_samples: list[int] = []
        for repeat in range(1, 6):
            name = new_app(f"startup-{repeat}")
            _, elapsed = startup(name)
            start_samples.append(elapsed)
            time.sleep(IDLE_SETTLE_SECONDS)
            mem = proc_memory(name)
            idle_samples.append(mem["VmRSS"])
            records.append({"metric": "startup_to_health", "scenario": "migrated_metadata", "repeat": repeat,
                            "value": elapsed, "unit": "seconds", "notes": "Container start to healthy /health."})
            records.append({"metric": "idle_rss", "scenario": "migrated_metadata", "repeat": repeat,
                            "value": mem["VmRSS"], "unit": "bytes", "notes": f"PID 1 VmRSS after {IDLE_SETTLE_SECONDS}s idle."})
            docker_rm(name)

        peak_samples: dict[str, list[int]] = {"small_query": [], "bounded_stream": []}
        for scenario in ("small_query", "bounded_stream"):
            for repeat in range(1, 4):
                name = new_app(f"{scenario}-{repeat}")
                base, _ = startup(name)
                time.sleep(IDLE_SETTLE_SECONDS)
                before = proc_memory(name)
                if scenario == "small_query":
                    fetch_small(base, token, source_id)
                    batches = 1
                else:
                    batches = fetch_stream(base, token, source_id, ROWS)
                after = proc_memory(name)
                peak_samples[scenario].append(after["VmHWM"])
                records.append({"metric": "pre_query_rss", "scenario": scenario, "repeat": repeat,
                                "value": before["VmRSS"], "unit": "bytes", "notes": "PID 1 VmRSS before workload."})
                records.append({"metric": "query_process_peak_rss", "scenario": scenario, "repeat": repeat,
                                "value": after["VmHWM"], "unit": "bytes",
                                "notes": f"PID 1 VmHWM after {ROWS if scenario == 'bounded_stream' else 1} row(s); batch_size={BATCH_SIZE if scenario == 'bounded_stream' else 10}; batches={batches}."})
                docker_rm(name)

        date = datetime.now(timezone.utc).strftime("%Y-%m-%d")
        raw = Path(os.environ.get("DATA_FABRIC_FOOTPRINT_RAW_OUTPUT",
                                  ROOT / f"docs/performance/results/deployment_footprint_{date}.csv"))
        raw.parent.mkdir(parents=True, exist_ok=True)
        with raw.open("w", newline="", encoding="utf-8") as output:
            writer = csv.DictWriter(output, fieldnames=("metric", "scenario", "repeat", "value", "unit", "notes"), lineterminator="\n")
            writer.writeheader()
            writer.writerows(records)
            summaries = [
                ("artifact_size", "release_binary", binary_size, "bytes", "Extracted from the image built by Dockerfile."),
                ("artifact_size", "image_uncompressed", image_size, "bytes", f"Image ID {image_info['Id']}."),
                ("startup_median", "migrated_metadata", statistics.median(start_samples), "seconds", "Median of five restarts."),
                ("idle_rss_median", "migrated_metadata", statistics.median(idle_samples), "bytes", "Median after idle settling period."),
                ("query_peak_rss_median", "small_query", statistics.median(peak_samples["small_query"]), "bytes", "Median of three fresh container processes."),
                ("query_peak_rss_median", "bounded_stream", statistics.median(peak_samples["bounded_stream"]), "bytes", "Median of three fresh container processes."),
            ]
            for metric, scenario, value, unit, notes in summaries:
                writer.writerow({"metric": metric, "scenario": scenario, "repeat": 1, "value": value, "unit": unit, "notes": notes})

        metadata = {
            "run_at_utc": datetime.now(timezone.utc).isoformat(),
            "source_commit": cmd(["git", "rev-parse", "HEAD"], "reading source commit"),
            "docker_server_version": cmd(["docker", "version", "--format", "{{.Server.Version}}"], "reading Docker version"),
            "os": platform.platform(), "architecture": platform.machine(), "logical_cpus": os.cpu_count(),
            "cpu_model": next((line.split(":", 1)[1].strip() for line in Path("/proc/cpuinfo").read_text().splitlines() if line.lower().startswith("model name")), platform.processor()),
            "host_memory_total_bytes": next(int(line.split()[1]) * 1024 for line in Path("/proc/meminfo").read_text().splitlines() if line.startswith("MemTotal:")),
            "go_builder": "golang:1.26.1 (Dockerfile)",
            "build_mode": "CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags=\'-s -w\' -o /app/main ./cmd/server (builder architecture)",
            "mysql_version": mysql_version, "mysql_image": MYSQL_IMAGE, "mysql_image_id": mysql_image_id,
            "mysql_repo_digests": json.loads(cmd(["docker", "image", "inspect", MYSQL_IMAGE, "--format", "{{json .RepoDigests}}"], "reading MySQL repository digest")),
            "source_rows": ROWS, "payload_bytes": PAYLOAD_BYTES, "stream_batch_size": BATCH_SIZE,
            "startup_repeats": 5, "query_repeats": 3, "idle_settle_seconds": IDLE_SETTLE_SECONDS,
            "binary_size_bytes": binary_size, "container_image_size_uncompressed_bytes": image_size,
            "container_image_id": image_info["Id"],
        }
        meta_path = raw.with_suffix(".json")
        meta_path.write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
        mib = 1024 * 1024
        print(f"source_commit={metadata['source_commit']}")
        print(f"mysql={mysql_version} image_id={mysql_image_id}")
        print(f"release_binary_bytes={binary_size}")
        print(f"container_image_uncompressed_bytes={image_size}")
        print(f"first_start_to_health_seconds={first_start:.3f}")
        print(f"restart_to_health_median_seconds={statistics.median(start_samples):.3f}; samples={','.join(f'{v:.3f}' for v in start_samples)}")
        print(f"idle_rss_median_mib={statistics.median(idle_samples)/mib:.2f}; samples={','.join(f'{v/mib:.2f}' for v in idle_samples)}")
        for scenario, values in peak_samples.items():
            print(f"{scenario}_vmhwm_median_mib={statistics.median(values)/mib:.2f}; samples={','.join(f'{v/mib:.2f}' for v in values)}")
        print(f"raw_csv={raw}")
        print(f"metadata_json={meta_path}")
        return 0
    finally:
        for container in reversed(containers):
            docker_rm(container)
        subprocess.run(["docker", "image", "rm", "-f", image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        subprocess.run(["docker", "network", "rm", network], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"deployment footprint benchmark failed: {error}", file=sys.stderr)
        raise SystemExit(1)
