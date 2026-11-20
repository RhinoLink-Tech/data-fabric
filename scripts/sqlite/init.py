#!/usr/bin/env python3
"""Initialize an isolated local SQLite profile without replacing existing keys."""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "mcp"))
import onboard

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_DIRECTORY = ROOT / "deploy" / "sqlite"


def metadata_volume_exists(project):
    try:
        result = subprocess.run(
            ["docker", "volume", "ls", "--format", "{{.Name}}"],
            capture_output=True, text=True, timeout=15, check=True,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise onboard.OnboardingError("Docker must be available to check for an existing metadata volume") from exc
    return project + "_metadata" in result.stdout.splitlines()


def initialize(directory, project):
    if not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", project):
        raise onboard.OnboardingError("project name must contain lowercase letters, numbers, hyphens or underscores")
    directory = directory.expanduser().resolve()
    if not directory.is_dir():
        raise onboard.OnboardingError("deployment directory does not exist")
    env_path, example = directory / ".env", directory / ".env.example"
    if env_path.is_symlink():
        raise onboard.OnboardingError("refusing to use a symlink for .env")
    if env_path.exists():
        if not env_path.is_file() or stat.S_IMODE(env_path.stat().st_mode) & 0o077:
            raise onboard.OnboardingError("existing .env must be a regular owner-only file")
        values = onboard.parse_env(env_path)
        secret = values.get("JWT_SECRET", "")
        if onboard.is_placeholder(secret) or len(secret.encode("utf-8")) < 32:
            raise onboard.OnboardingError("existing JWT_SECRET is missing or too short; restore the original configuration")
        ring, active = onboard.validate_credential_keyring(
            values.get("DATA_FABRIC_CREDENTIAL_KEYS", ""),
            values.get("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID", ""),
        )
        if ring is None:
            raise onboard.OnboardingError("existing credential keyring is missing; restore the original configuration")
        try:
            gid = int(values.get("DATA_FABRIC_TLS_GID", ""))
            if gid < 0:
                raise ValueError
        except ValueError as exc:
            raise onboard.OnboardingError("existing DATA_FABRIC_TLS_GID is invalid") from exc
        onboard.init_tls(directory, gid)
        return "Kept existing configuration and TLS files."
    if metadata_volume_exists(project):
        raise onboard.OnboardingError("metadata volume already exists but .env is missing; restore its original keys before starting")
    if example.is_symlink() or not example.is_file():
        raise onboard.OnboardingError(".env.example must be an existing regular file")
    values = {
        "JWT_SECRET": secrets.token_urlsafe(48),
        "DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID": "key-1",
        "DATA_FABRIC_CREDENTIAL_KEYS": json.dumps({"key-1": base64.b64encode(secrets.token_bytes(32)).decode("ascii")}, separators=(",", ":")),
        "DATA_FABRIC_TLS_GID": str(os.getgid()),
    }
    onboard.init_tls(directory, os.getgid())
    content = onboard.replace_env_values(onboard.read_env_text(example), values)
    try:
        fd = os.open(env_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError as exc:
        raise onboard.OnboardingError(".env appeared during initialization; inspect it and retry") from exc
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as out:
            out.write(content)
            out.flush()
            os.fsync(out.fileno())
    except Exception:
        # A partial file is deliberately retained: never silently replace
        # possibly active credentials on a subsequent invocation.
        raise
    return "Created private local configuration and TLS files."


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, default=DEFAULT_DIRECTORY)
    parser.add_argument("--project-name", default="data-fabric-sqlite")
    args = parser.parse_args()
    try:
        print(initialize(args.directory, args.project_name))
    except (onboard.OnboardingError, OSError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
