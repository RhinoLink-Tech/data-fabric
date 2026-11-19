#!/usr/bin/env python3
"""Compare the full registry and two experimental restricted registries offline.

This is a sizing experiment, not a supported Core build. It never edits the
checkout, uses no Docker, and fails rather than downloading Go dependencies.
Run from a clean tracked revision with a cached Go toolchain and modules.
"""
import argparse
import gzip
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
REGISTRY = Path("internal/database/driver_registry.go")
PACKAGES = ("domestic", "file_system", "nosql", "object_storage", "olap", "table_formats", "warehouses")


def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="go", help="Path to a locally installed Go 1.26.1+ executable")
    args = parser.parse_args()
    env = os.environ.copy()
    env.update({"GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local",
                "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64"})
    version = run([args.go, "version"], capture_output=True, text=True, env=env).stdout.strip()
    revision = run(["git", "rev-parse", "HEAD"], cwd=ROOT, capture_output=True, text=True).stdout.strip()
    tree = run(["git", "rev-parse", "HEAD^{tree}"], cwd=ROOT, capture_output=True, text=True).stdout.strip()
    if run(["git", "status", "--porcelain", "--untracked-files=no"], cwd=ROOT, capture_output=True, text=True).stdout.strip():
        raise SystemExit("tracked checkout changes are present; commit them before benchmarking")
    result = {"revision": revision, "tree": tree, "go": version,
              "flags": "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 -buildvcs=false -trimpath -ldflags=-s -w",
              "measurements": {}}
    with tempfile.TemporaryDirectory(prefix="data-fabric-registry-size-") as temp:
        source = Path(temp) / "source"
        source.mkdir()
        archive = Path(temp) / "source.tar"
        with archive.open("wb") as output:
            run(["git", "archive", "HEAD"], cwd=ROOT, stdout=output)
        with tarfile.open(archive) as bundle:
            bundle.extractall(source, filter="data")
        archive.unlink()
        registry = source / REGISTRY
        original = registry.read_text(encoding="utf-8")

        def build(name):
            target = Path(temp) / name
            times = []
            for _ in range(2):
                started = time.monotonic()
                run([args.go, "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w", "-o", str(target), "./cmd/server"], cwd=source, env=env)
                times.append(round(time.monotonic() - started, 3))
            gz = Path(str(target) + ".gz")
            with target.open("rb") as input_file, gzip.open(gz, "wb", compresslevel=6) as output_file:
                shutil.copyfileobj(input_file, output_file, 1024 * 1024)
            result["measurements"][name] = {"binary_bytes": target.stat().st_size,
                                             "gzip_bytes": gz.stat().st_size,
                                             "build_seconds": times}

        build("full")

        def restricted(include_trino):
            candidate = original
            for package in PACKAGES:
                if include_trino and package == "warehouses":
                    continue
                line = f'\t"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/{package}"\n'
                if candidate.count(line) != 1:
                    raise SystemExit(f"registry import changed: {package}")
                candidate = candidate.replace(line, "")
            start = candidate.index("func (dr *DriverRegistry) registerDrivers() {")
            end = candidate.index("func (dr *DriverRegistry) register(", start)
            replacement = """func (dr *DriverRegistry) registerDrivers() {
 dr.mutex.Lock()
 defer dr.mutex.Unlock()
 dr.register(model.DatabaseTypeMySQL, func() drivers.Driver { return &traditional.MySQLDriver{} })
 dr.register(model.DatabaseTypeMariaDB, func() drivers.Driver { return &traditional.MySQLDriver{} })
 dr.register(model.DatabaseTypePostgreSQL, func() drivers.Driver { return &traditional.PostgreSQLDriver{} })
"""
            if include_trino:
                replacement += " dr.register(model.DatabaseTypeTrino, func() drivers.Driver { return warehouses.NewTrinoDriver() })\n"
            replacement += "}\n\n"
            registry.write_text(candidate[:start] + replacement + candidate[end:], encoding="utf-8")

        restricted(False)
        build("restricted_registry")
        restricted(True)
        build("federated_registry")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
