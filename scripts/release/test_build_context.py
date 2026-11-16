#!/usr/bin/env python3
"""Verify Docker's real build-context filtering with synthetic fixtures only."""

import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


def main():
    repository = pathlib.Path(__file__).resolve().parents[2]
    excluded = (
        ".env", "configs/config.yaml", "deploy/mcp/.env", "deploy/mcp/.env.backup",
        "nested/service/.env", "nested/service/.env.local", "deploy/mcp/tls/tls.key",
        "deploy/mcp/tls/tls.crt", "deploy/mcp/recovery/metadata.sql",
        "deploy/mcp/recovery/deployment.env", "nested/private.key", "nested/private.pem",
        "nested/private.p12", "nested/private.pfx", "nested/profile.age",
    )
    included = (
        "deploy/mcp/.env.example", "nested/service/.env.example",
        "configs/config.yaml.example", "main.go", "deploy/mcp/sample-source-init.sql",
    )
    with tempfile.TemporaryDirectory(prefix="data-fabric-build-context-") as tmp:
        root = pathlib.Path(tmp)
        context, output = root / "context", root / "output"
        context.mkdir()
        shutil.copyfile(repository / ".dockerignore", context / ".dockerignore")
        (context / "Dockerfile").write_text("FROM scratch\nCOPY . /\n", encoding="utf-8")
        for name in excluded + included:
            target = context / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("SYNTHETIC_BUILD_CONTEXT_TEST_ONLY\n", encoding="utf-8")
        subprocess.run(
            ["docker", "buildx", "build", "--network=none", "--output", f"type=local,dest={output}", str(context)],
            check=True,
        )
        leaked = [name for name in excluded if (output / name).exists()]
        missing = [name for name in included if not (output / name).is_file()]
        if leaked or missing:
            raise SystemExit(f"build context mismatch: excluded files present={leaked}; required files missing={missing}")
    print("Synthetic Docker build context excludes deployment material and retains required templates.")


class DockerBuildContextTest(unittest.TestCase):
    def test_deployment_material_is_excluded(self):
        if shutil.which("docker") is None:
            if os.environ.get("CI"):
                self.fail("Docker is required for the CI build-context regression test")
            self.skipTest("Docker is unavailable")
        main()


if __name__ == "__main__":
    unittest.main()
