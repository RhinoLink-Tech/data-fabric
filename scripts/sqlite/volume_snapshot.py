#!/usr/bin/env python3
"""Back up and restore the experimental SQLite Compose metadata volume."""
import argparse
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
VOLUME = "/var/lib/data-fabric"
ENV_FILE = ROOT / "deploy/sqlite/.env"


class SnapshotError(Exception):
    pass


def compose(project, args, *, stdin=None, stdout=subprocess.PIPE):
    command = ["docker", "compose", "--project-name", project, "--env-file", str(ENV_FILE), "--file",
               str(ROOT / "deploy/sqlite/compose.yml"), *args]
    try:
        result = subprocess.run(command, stdin=stdin, stdout=stdout,
                                stderr=subprocess.PIPE, check=False)
    except OSError as exc:
        raise SnapshotError("Docker Compose could not be started") from exc
    if result.returncode:
        # Compose diagnostics can contain configuration values; do not echo them.
        raise SnapshotError("Docker Compose operation failed; check Docker and the profile configuration")
    return result.stdout


def run(project, *args, stdin=None, stdout=subprocess.PIPE):
    return compose(project, ["run", "--rm", "--no-deps", "--pull", "never", "-T", "api", *args],
                   stdin=stdin, stdout=stdout)


def remove_temporary(project, name):
    compose(project, ["run", "--rm", "--no-deps", "--pull", "never", "-T", "--entrypoint", "rm",
                      "api", "-f", "--", f"{VOLUME}/{name}"])


def validate_local_parent(path):
    parent = path.parent.resolve(strict=True)
    info = parent.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise SnapshotError("backup directory must be owned by the current user and mode 0700 or stricter")


def main():
    global ENV_FILE
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project-name", default="data-fabric-sqlite")
    parser.add_argument("--env-file", type=Path, default=ENV_FILE, help="original private profile configuration")
    parser.add_argument("--database", default="metadata.sqlite", help="database filename in the metadata volume")
    sub = parser.add_subparsers(dest="action", required=True)
    backup = sub.add_parser("backup")
    backup.add_argument("output", type=Path)
    restore = sub.add_parser("restore")
    restore.add_argument("input", type=Path)
    restore.add_argument("--output-name", required=True, help="new database filename in the metadata volume")
    args = parser.parse_args()
    ENV_FILE = args.env_file.expanduser().resolve(strict=True)
    if not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", args.project_name):
        raise SnapshotError("invalid Compose project name")
    def filename(value):
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*\.sqlite", value):
            raise SnapshotError("database names must be simple .sqlite filenames")
        return value
    source_name = filename(args.database)
    temporary = ".snapshot-" + secrets.token_hex(16) + ".sqlite"
    if args.action == "backup":
        validate_local_parent(args.output)
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        created = False
        try:
            run(args.project_name, "sqlite-metadata", "backup", "--source", f"{VOLUME}/{source_name}",
                "--output", f"{VOLUME}/{temporary}")
            created = True
            with os.fdopen(fd, "wb") as output:
                fd = -1
                compose(args.project_name, ["run", "--rm", "--no-deps", "--pull", "never", "-T", "--entrypoint", "cat", "api", f"{VOLUME}/{temporary}"], stdout=output)
                output.flush()
                os.fsync(output.fileno())
        except BaseException:
            if fd != -1:
                os.close(fd)
            args.output.unlink(missing_ok=True)
            raise
        finally:
            if created:
                remove_temporary(args.project_name, temporary)
        print("Snapshot saved. Back up the original credential keys separately.")
    else:
        output_name = filename(args.output_name)
        running = compose(args.project_name, ["ps", "--status", "running", "--services"])
        if running.strip():
            raise SnapshotError("stop all Compose services before restoring")
        flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
        fd = os.open(args.input, flags)
        created = False
        try:
            if not stat.S_ISREG(os.fstat(fd).st_mode):
                raise SnapshotError("snapshot must be a regular file")
            with os.fdopen(fd, "rb") as inp:
                fd = -1
                # A random temporary name plus shell noclobber avoids replacing volume files.
                compose(args.project_name, ["run", "--rm", "--no-deps", "--pull", "never", "-T", "--entrypoint", "sh", "api", "-c",
                    'umask 077; set -C; cat > "$1"', "sh", f"{VOLUME}/{temporary}"], stdin=inp)
                created = True
            run(args.project_name, "sqlite-metadata", "restore", "--source", f"{VOLUME}/{temporary}",
                "--output", f"{VOLUME}/{output_name}")
        finally:
            if fd != -1:
                os.close(fd)
            if created:
                remove_temporary(args.project_name, temporary)
        print(f"Restored to {VOLUME}/{output_name}; use the original credential keyring before starting services.")


if __name__ == "__main__":
    try:
        main()
    except (SnapshotError, OSError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        sys.exit(1)
