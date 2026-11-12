#!/usr/bin/env python3
"""Create, verify, and stage an encrypted backup of the self-hosted MCP profile."""
from __future__ import annotations

import argparse
import errno
import base64
import binascii
import hashlib
import json
import os
import re
import shutil
import socket
import stat
import subprocess
import sys
import tarfile
import tempfile
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_DEPLOY_DIR = ROOT / "deploy" / "mcp"
FORMAT = "data-fabric-mcp-profile-v1"
ALLOWED_MEMBERS = {"manifest.json", "metadata.sql", "deployment.env"}
APP_SERVICES = ("data-fabric-api", "data-fabric-mcp")


class RecoveryError(Exception):
    pass


def parse_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    duplicates: set[str] = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        match = re.match(r"^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$", line)
        if not match:
            continue
        key, value = match.groups()
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            if value[0] == '"':
                try:
                    value = json.loads(value)
                except json.JSONDecodeError as exc:
                    raise RecoveryError(f".env contains invalid quoted value for {key}") from exc
            else:
                value = value[1:-1]
        if key in values:
            duplicates.add(key)
        values[key] = value
    if duplicates:
        raise RecoveryError(".env contains duplicate keys: " + ", ".join(sorted(duplicates)))
    return values


def replace_env_values(text: str, replacements: dict[str, str]) -> str:
    output: list[str] = []
    seen: set[str] = set()
    for line in text.splitlines():
        match = re.match(r"^(\s*)([A-Za-z_][A-Za-z0-9_]*)(\s*=)(.*)$", line)
        if not match:
            output.append(line)
            continue
        leading, key, separator, _ = match.groups()
        if key in replacements:
            output.append(f"{leading}{key}{separator}{replacements[key]}")
            seen.add(key)
        else:
            output.append(line)
    for key, value in replacements.items():
        if key not in seen:
            output.append(f"{key}={value}")
    return "\n".join(output).rstrip() + "\n"


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require_private_file(path: Path, label: str) -> None:
    if path.is_symlink() or not path.is_file():
        raise RecoveryError(f"{label} must be a regular file, not a symlink")
    info = path.stat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600:
        raise RecoveryError(f"{label} must be owned by the current user and have mode 0600")


def reject_symlink_components(path: Path) -> Path:
    path = Path(os.path.abspath(path.expanduser()))
    for component in (path, *path.parents):
        if component.is_symlink():
            raise RecoveryError("sensitive path must not contain symlink components")
    return path

def secure_staging_root(path: Path) -> Path:
    path = reject_symlink_components(path)
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    mode = stat.S_IMODE(path.stat().st_mode) if path.is_dir() else 0
    if path.stat().st_uid != os.getuid() or (mode & 0o700) != 0o700 or (mode & 0o077):
        raise RecoveryError("staging directory must be owned by the current user and have mode 0700")
    return path


def secure_tempdir(root: Path, prefix: str) -> Path:
    path = Path(tempfile.mkdtemp(prefix=prefix, dir=root))
    os.chmod(path, 0o700)
    return path


def run(args: list[str], *, cwd: Path | None = None, output=None, timeout=None) -> subprocess.CompletedProcess:
    try:
        return subprocess.run(args, cwd=cwd, stdout=output if output is not None else subprocess.PIPE,
                              stderr=subprocess.PIPE, text=output is None, timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RecoveryError(f"could not run {Path(args[0]).name}; verify the required local tool is available") from exc


def git_revision(project_root: Path) -> str:
    result = run(["git", "-C", str(project_root), "rev-parse", "--verify", "HEAD"])
    if result.returncode or not re.fullmatch(r"[0-9a-f]{40,64}", result.stdout.strip()):
        raise RecoveryError("candidate source revision could not be determined")
    status = run(["git", "-C", str(project_root), "status", "--porcelain", "--untracked-files=all"])
    if status.returncode or status.stdout.strip():
        raise RecoveryError("source changes or untracked files are present; use a clean candidate checkout")
    return result.stdout.strip()


def validate_keyring(values: dict[str, str]) -> tuple[str, list[str]]:
    active = values.get("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID", "").strip()
    try:
        ring = json.loads(values.get("DATA_FABRIC_CREDENTIAL_KEYS", ""))
        if not isinstance(ring, dict) or not active or active not in ring or not ring:
            raise ValueError
        for key_id, encoded in ring.items():
            if not isinstance(key_id, str) or not key_id or ":" in key_id or not isinstance(encoded, str):
                raise ValueError
            key = base64.b64decode(encoded, validate=True)
            if len(key) != 32:
                raise ValueError
    except (json.JSONDecodeError, ValueError, binascii.Error, TypeError) as exc:
        raise RecoveryError("credential keyring is invalid or missing an active AES-256 key") from exc
    return active, sorted(ring)


def candidate_image_identity(directory: Path, env_path: Path, project_name: str, running: list[str], image_ref: str,
                             source_revision: str) -> tuple[str, list[str]]:
    active_apps = [service for service in APP_SERVICES if service in running]
    image_id = ""
    if active_apps and len(active_apps) != len(APP_SERVICES):
        raise RecoveryError("API and MCP must be either both running or both stopped before backup")
    if active_apps:
        observed: set[tuple[str, str]] = set()
        for service in APP_SERVICES:
            container = compose_result(directory, env_path, project_name, "ps", "-q", service).stdout.strip()
            details = run(["docker", "inspect", "--format", "{{.Image}}|{{.Config.Image}}", container], timeout=60)
            if details.returncode or "|" not in details.stdout:
                raise RecoveryError("running application image identity could not be recorded")
            container_image_id, configured_ref = details.stdout.strip().split("|", 1)
            observed.add((container_image_id, configured_ref))
        if len(observed) != 1:
            raise RecoveryError("API and MCP are not using the same candidate image")
        image_id, configured_ref = next(iter(observed))
        if configured_ref != image_ref:
            raise RecoveryError("running application image does not match DATA_FABRIC_IMAGE in .env")
    else:
        inspected = run(["docker", "image", "inspect", "--format", "{{.Id}}", image_ref], timeout=60)
        if inspected.returncode:
            raise RecoveryError("configured candidate image is not available locally; pull/build the exact candidate before backup")
        image_id = inspected.stdout.strip()
    inspected = run(["docker", "image", "inspect", "--format", '{{index .Config.Labels "org.opencontainers.image.revision"}}|{{json .RepoDigests}}', image_id], timeout=60)
    if inspected.returncode or not image_id.startswith("sha256:") or "|" not in inspected.stdout:
        raise RecoveryError("candidate image provenance could not be recorded")
    revision_label, encoded_digests = inspected.stdout.strip().split("|", 1)
    if revision_label != source_revision:
        raise RecoveryError("candidate image revision label does not match the clean source checkout")
    try:
        repo_digests = json.loads(encoded_digests)
    except json.JSONDecodeError as exc:
        raise RecoveryError("candidate image registry digests could not be recorded") from exc
    return image_id, repo_digests if isinstance(repo_digests, list) else []


def compose_command(directory: Path, env_file: Path, project_name: str, *args: str) -> list[str]:
    return ["docker", "compose", "--project-name", project_name, "--env-file", str(env_file),
            "-f", "docker-compose.yml", *args]


def compose_result(directory: Path, env_file: Path, project_name: str, *args: str) -> subprocess.CompletedProcess:
    result = run(compose_command(directory, env_file, project_name, *args), cwd=directory, timeout=60)
    if result.returncode:
        raise RecoveryError("Docker Compose operation failed; inspect the selected profile locally without sharing secret output")
    return result


def compose_services(directory: Path, env_file: Path, project_name: str) -> dict[str, dict]:
    output = compose_result(directory, env_file, project_name, "ps", "--format", "json").stdout.strip()
    if not output:
        return {}
    try:
        rows = json.loads(output)
        if isinstance(rows, dict):
            rows = [rows]
        if not isinstance(rows, list):
            raise ValueError
    except (json.JSONDecodeError, ValueError):
        try:
            rows = [json.loads(line) for line in output.splitlines() if line.strip()]
        except json.JSONDecodeError as exc:
            raise RecoveryError("Docker Compose returned an unrecognized service status format") from exc
    result: dict[str, dict] = {}
    for row in rows:
        if not isinstance(row, dict):
            raise RecoveryError("Docker Compose returned an ambiguous service status")
        service = row.get("Service") or row.get("Name")
        if isinstance(service, str):
            result[service] = row
    return result


def service_running_and_healthy(row: dict | None) -> tuple[bool, bool]:
    if not row:
        return False, False
    state = str(row.get("State") or "").lower()
    status = str(row.get("Status") or "").lower()
    health = str(row.get("Health") or "").lower()
    running = state == "running" or status.startswith("up ")
    healthy = health == "healthy" or "(healthy)" in status
    if "unhealthy" in status or "starting" in status:
        healthy = False
    return running, healthy


def backup(args: argparse.Namespace) -> int:
    directory = reject_symlink_components(args.directory)
    validate_project_name(args.project_name)
    env_path = directory / ".env"
    require_private_file(env_path, "deployment .env")
    if not (directory / "docker-compose.yml").is_file():
        raise RecoveryError("deployment directory has no docker-compose.yml")
    values = parse_env(env_path)
    active_key, key_ids = validate_keyring(values)
    source_revision = git_revision(directory.parent.parent)
    if values.get("DATA_FABRIC_SOURCE_REVISION", "").strip() != source_revision:
        raise RecoveryError(".env source revision does not match the clean checkout; run onboard.py init and rebuild the candidate")

    output = reject_symlink_components(args.output)
    if output.suffix != ".age" or output.exists() or output.is_symlink():
        raise RecoveryError("output must be a new .age file; existing files are never overwritten")
    if not output.parent.is_dir() or output.resolve().is_relative_to(directory):
        raise RecoveryError("output parent must exist and the encrypted backup must be outside the deployment directory")
    if not args.recipient.strip():
        raise RecoveryError("an age recipient is required")
    if not shutil.which("age"):
        raise RecoveryError("age is required to encrypt profile backups")
    staging_root = secure_staging_root(args.staging_dir)
    work = secure_tempdir(staging_root, "data-fabric-backup-")
    encrypted_tmp: Path | None = None
    running_to_restore: list[str] = []
    restart_error = False
    try:
        image_ref = values.get("DATA_FABRIC_IMAGE", "data-fabric:local").strip() or "data-fabric:local"
        service_rows = compose_services(directory, env_path, args.project_name)
        db_running, db_healthy = service_running_and_healthy(service_rows.get("metadata-db"))
        if not db_running or not db_healthy:
            raise RecoveryError("metadata-db must be running and healthy before backup")
        running = compose_result(directory, env_path, args.project_name, "ps", "--services", "--status", "running").stdout.splitlines()
        active_apps = [service for service in APP_SERVICES if service in running]
        if active_apps and len(active_apps) != len(APP_SERVICES):
            raise RecoveryError("API and MCP must be either both running or both stopped before backup")
        if active_apps and any(not service_running_and_healthy(service_rows.get(service))[1] for service in APP_SERVICES):
            raise RecoveryError("API and MCP must both be healthy before backup")
        db_container = compose_result(directory, env_path, args.project_name, "ps", "-q", "metadata-db").stdout.strip()
        db_image = run(["docker", "inspect", "--format", "{{.Image}}|{{.Config.Image}}", db_container], timeout=60)
        if db_image.returncode or "|" not in db_image.stdout:
            raise RecoveryError("metadata database image identity could not be recorded")
        db_image_id, db_image_ref = db_image.stdout.strip().split("|", 1)
        image_id, repo_digests = candidate_image_identity(directory, env_path, args.project_name, running, image_ref, source_revision)
        running_to_restore = [service for service in APP_SERVICES if service in running]
        if running_to_restore:
            compose_result(directory, env_path, args.project_name, "stop", "--timeout", "30", *running_to_restore)

        sql_path = work / "metadata.sql"
        dump = compose_command(directory, env_path, args.project_name, "exec", "-T", "metadata-db", "sh", "-ec",
                               'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqldump --user=root --single-transaction --skip-comments --routines --events --triggers --databases data_fabric')
        try:
            with sql_path.open("wb") as sql_file:
                os.chmod(sql_path, 0o600)
                result = subprocess.run(dump, cwd=directory, stdout=sql_file, stderr=subprocess.PIPE, timeout=None, check=False)
        except OSError as exc:
            raise RecoveryError("could not create the metadata snapshot") from exc
        if result.returncode or sql_path.stat().st_size == 0:
            raise RecoveryError("metadata snapshot failed or was empty; no backup was published")

        version_query = compose_command(directory, env_path, args.project_name, "exec", "-T", "metadata-db", "sh", "-ec",
                                        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root --batch --skip-column-names --execute="SELECT VERSION()"')
        version_result = run(version_query, cwd=directory, timeout=60)
        if version_result.returncode or not version_result.stdout.strip():
            raise RecoveryError("metadata MySQL version could not be recorded")

        env_copy = work / "deployment.env"
        shutil.copyfile(env_path, env_copy)
        os.chmod(env_copy, 0o600)
        manifest = {
            "format": FORMAT,
            "created_at_utc": datetime.now(timezone.utc).isoformat(),
            "source_revision": source_revision,
            "app_image_reference": image_ref,
            "app_image_id": image_id,
            "app_repo_digests": repo_digests,
            "metadata_database": "data_fabric",
            "metadata_mysql_version": version_result.stdout.strip(),
            "metadata_mysql_image_reference": db_image_ref,
            "metadata_mysql_image_id": db_image_id,
            "credential_active_key_id": active_key,
            "credential_key_ids": key_ids,
            "files": {
                "metadata.sql": {"sha256": file_sha256(sql_path), "bytes": sql_path.stat().st_size},
                "deployment.env": {"sha256": file_sha256(env_copy), "bytes": env_copy.stat().st_size},
            },
            "excluded": ["business-source databases", "TLS private key (regenerate on restore)", "logs", "query results"],
        }
        manifest_path = work / "manifest.json"
        manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        os.chmod(manifest_path, 0o600)
        tar_path = work / "backup.tar.gz"
        with tarfile.open(tar_path, mode="w:gz") as archive:
            for name in ("manifest.json", "metadata.sql", "deployment.env"):
                archive.add(work / name, arcname=name, recursive=False)
        os.chmod(tar_path, 0o600)

        fd, tmp_name = tempfile.mkstemp(prefix=f".{output.name}.", suffix=".tmp", dir=output.parent)
        encrypted_tmp = Path(tmp_name)
        try:
            result = subprocess.run(["age", "--encrypt", "--recipient", args.recipient, str(tar_path)],
                                    stdout=fd, stderr=subprocess.PIPE, check=False)
        except OSError as exc:
            raise RecoveryError("age encryption failed; no backup was published") from exc
        finally:
            os.close(fd)
        if result.returncode or encrypted_tmp.stat().st_size == 0:
            raise RecoveryError("age encryption failed; no backup was published")
        os.chmod(encrypted_tmp, 0o600)
        with encrypted_tmp.open("rb+") as stream:
            os.fsync(stream.fileno())
        try:
            os.link(encrypted_tmp, output)
        except FileExistsError as exc:
            raise RecoveryError("backup output appeared during creation; existing files were preserved") from exc
        except OSError as exc:
            if exc.errno not in (errno.EPERM, errno.EOPNOTSUPP, errno.ENOSYS, errno.EXDEV):
                raise RecoveryError("could not safely publish the encrypted backup") from exc
            try:
                fd = os.open(output, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                with os.fdopen(fd, "wb") as target, encrypted_tmp.open("rb") as source:
                    shutil.copyfileobj(source, target, length=1024 * 1024)
                    target.flush()
                    os.fsync(target.fileno())
            except FileExistsError as race:
                raise RecoveryError("backup output appeared during creation; existing files were preserved") from race
            except OSError as copy_error:
                output.unlink(missing_ok=True)
                raise RecoveryError("could not safely publish the encrypted backup") from copy_error
        directory_fd = os.open(output.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
        encrypted_tmp.unlink()
        encrypted_tmp = None
        print(f"Encrypted backup created: {output}")
        print(f"Candidate source: {source_revision}; app image ID: {image_id}")
        print(f"MySQL: {version_result.stdout.strip()}; credential key IDs: {', '.join(key_ids)}")
        print(f"Metadata DB image: {db_image_ref} ({db_image_id})")
        print(f"Encrypted bytes: {output.stat().st_size}; SHA-256: {file_sha256(output)}")
    finally:
        try:
            if running_to_restore:
                try:
                    restart = run(compose_command(directory, env_path, args.project_name, "start", *running_to_restore), cwd=directory, timeout=60)
                    if restart.returncode:
                        restart_error = True
                except RecoveryError:
                    restart_error = True
                if restart_error:
                    print("WARNING: the backup command could not restart the API/MCP services; start them manually and inspect status.", file=sys.stderr)
        finally:
            try:
                if encrypted_tmp is not None:
                    encrypted_tmp.unlink(missing_ok=True)
            except OSError:
                print("WARNING: a temporary encrypted output file remains in the backup directory.", file=sys.stderr)
            finally:
                shutil.rmtree(work, ignore_errors=True)
    if restart_error:
        return 1
    return 0


def decrypt_archive(archive_path: Path, identity: Path, staging_root: Path) -> tuple[Path, Path]:
    if archive_path.is_symlink() or not archive_path.is_file():
        raise RecoveryError("backup archive must be a regular file, not a symlink")
    require_private_file(identity, "age identity file")
    if not shutil.which("age"):
        raise RecoveryError("age is required to decrypt the profile backup")
    work = secure_tempdir(staging_root, "data-fabric-verify-")
    tar_path = work / "backup.tar.gz"
    try:
        with tar_path.open("wb") as target:
            os.chmod(tar_path, 0o600)
            result = subprocess.run(["age", "--decrypt", "--identity", str(identity), str(archive_path)],
                                    stdout=target, stderr=subprocess.PIPE, check=False)
        if result.returncode or tar_path.stat().st_size == 0:
            raise RecoveryError("backup could not be decrypted; verify the selected archive and age identity")
        payload = work / "payload"
        payload.mkdir(mode=0o700)
        extract_archive(tar_path, payload)
        return work, payload
    except BaseException:
        shutil.rmtree(work, ignore_errors=True)
        raise


def extract_archive(tar_path: Path, destination: Path) -> None:
    try:
        with tarfile.open(tar_path, mode="r:gz") as archive:
            members = archive.getmembers()
            if {member.name for member in members} != ALLOWED_MEMBERS or len(members) != len(ALLOWED_MEMBERS):
                raise RecoveryError("backup archive has missing or unexpected members")
            for member in members:
                path = PurePosixPath(member.name)
                if path.is_absolute() or ".." in path.parts or not member.isfile():
                    raise RecoveryError("backup archive contains an unsafe member")
                source = archive.extractfile(member)
                if source is None:
                    raise RecoveryError("backup archive member could not be read")
                target = destination / member.name
                with source, target.open("xb") as output:
                    shutil.copyfileobj(source, output, length=1024 * 1024)
                os.chmod(target, 0o600)
    except (tarfile.TarError, OSError) as exc:
        raise RecoveryError("backup archive is invalid or incomplete") from exc


def load_and_validate_payload(payload: Path) -> dict:
    try:
        manifest = json.loads((payload / "manifest.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise RecoveryError("backup manifest is invalid") from exc
    if not isinstance(manifest, dict) or manifest.get("format") != FORMAT:
        raise RecoveryError("backup format is unsupported")
    if not isinstance(manifest.get("files"), dict):
        raise RecoveryError("backup manifest file inventory is invalid")
    source_revision = manifest.get("source_revision", "")
    if not isinstance(source_revision, str) or not re.fullmatch(r"[0-9a-f]{40,64}", source_revision):
        raise RecoveryError("backup candidate revision is invalid")
    for name in ("metadata.sql", "deployment.env"):
        metadata = manifest.get("files", {}).get(name)
        path = payload / name
        if not isinstance(metadata, dict) or not re.fullmatch(r"[0-9a-f]{64}", str(metadata.get("sha256", ""))) or not isinstance(metadata.get("bytes"), int):
            raise RecoveryError(f"backup integrity metadata is invalid for {name}")
        if metadata["bytes"] <= 0:
            raise RecoveryError(f"backup member is empty: {name}")
        if metadata.get("sha256") != file_sha256(path) or metadata.get("bytes") != path.stat().st_size:
            raise RecoveryError(f"backup integrity check failed for {name}")
    values = parse_env(payload / "deployment.env")
    active, key_ids = validate_keyring(values)
    if manifest.get("credential_active_key_id") != active or manifest.get("credential_key_ids") != key_ids:
        raise RecoveryError("backup keyring inventory does not match its protected configuration")
    if not isinstance(manifest.get("app_image_id"), str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", manifest["app_image_id"]):
        raise RecoveryError("backup candidate image identity is invalid")
    if not isinstance(manifest.get("metadata_mysql_image_id"), str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", manifest["metadata_mysql_image_id"]):
        raise RecoveryError("backup metadata database image identity is invalid")
    if not isinstance(manifest.get("metadata_mysql_image_reference"), str) or not manifest["metadata_mysql_image_reference"]:
        raise RecoveryError("backup metadata database image reference is invalid")
    if not isinstance(manifest.get("app_image_reference"), str) or not manifest["app_image_reference"]:
        raise RecoveryError("backup application image reference is invalid")
    if not isinstance(manifest.get("metadata_mysql_version"), str) or not manifest["metadata_mysql_version"]:
        raise RecoveryError("backup metadata MySQL version is invalid")
    if not isinstance(manifest.get("credential_key_ids"), list) or not all(isinstance(item, str) for item in manifest["credential_key_ids"]):
        raise RecoveryError("backup key ID inventory is invalid")
    return manifest


def show_manifest(manifest: dict, archive_path: Path) -> None:
    print("Encrypted backup verified; no secret values were displayed.")
    print(f"Archive: {archive_path}; SHA-256: {file_sha256(archive_path)}")
    print(f"Created: {manifest.get('created_at_utc')}; source revision: {manifest['source_revision']}")
    print(f"Image: {manifest['app_image_reference']} ({manifest['app_image_id']})")
    print(f"Metadata MySQL: {manifest.get('metadata_mysql_version')}; DB image: {manifest['metadata_mysql_image_reference']} ({manifest['metadata_mysql_image_id']})")
    print(f"Credential key IDs: {', '.join(manifest['credential_key_ids'])}")
    print(f"Payload SHA-256: {manifest['files']['metadata.sql']['sha256']} (metadata SQL), {manifest['files']['deployment.env']['sha256']} (.env)")


def verify(args: argparse.Namespace) -> int:
    archive = reject_symlink_components(args.archive)
    identity = reject_symlink_components(args.identity_file)
    staging = secure_staging_root(args.staging_dir)
    work, payload = decrypt_archive(archive, identity, staging)
    try:
        manifest = load_and_validate_payload(payload)
        show_manifest(manifest, archive)
        return 0
    finally:
        shutil.rmtree(work, ignore_errors=True)


def validate_project_name(name: str) -> None:
    if not re.fullmatch(r"[a-z][a-z0-9_-]{2,55}", name):
        raise RecoveryError("Compose project name must start with a lowercase letter and use 3-56 lowercase letters, digits, _ or -")


def require_fresh_compose_project(project_name: str) -> None:
    projects = run(["docker", "compose", "ls", "--all", "--format", "json"], timeout=60)
    if projects.returncode:
        raise RecoveryError("could not verify the restore Compose project is unused; refusing to prepare files")
    try:
        rows = json.loads(projects.stdout or "[]")
    except json.JSONDecodeError as exc:
        raise RecoveryError("could not parse Compose project inventory; refusing to prepare files") from exc
    if not isinstance(rows, list) or any(not isinstance(row, dict) for row in rows):
        raise RecoveryError("could not parse Compose project inventory; refusing to prepare files")
    if any(row.get("Name", row.get("name")) == project_name for row in rows):
        raise RecoveryError("restore Compose project already exists")
    for resource in ("volume", "network"):
        found = run(["docker", resource, "ls", "--filter", f"label=com.docker.compose.project={project_name}", "--format", "{{.Name}}"], timeout=60)
        if found.returncode or found.stdout.strip():
            raise RecoveryError(f"restore Compose project already has {resource}s or its inventory is unavailable")


def require_free_loopback_port(port: int, label: str) -> None:
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
            probe.bind(("127.0.0.1", port))
    except OSError as exc:
        raise RecoveryError(f"{label} port {port} is already in use on loopback; choose another port") from exc


def restore(args: argparse.Namespace) -> int:
    archive = reject_symlink_components(args.archive)
    identity = reject_symlink_components(args.identity_file)
    staging = secure_staging_root(args.staging_dir)
    target = reject_symlink_components(args.target_deploy_dir)
    if not target.is_dir():
        raise RecoveryError("target deployment directory must be an existing candidate checkout directory")
    if not (target / "docker-compose.yml").is_file() or not (target / "docker-compose.recovery.yml").is_file() or not (target / ".env.example").is_file():
        raise RecoveryError("target directory is not a Data Fabric MCP deployment checkout")
    env_path = target / ".env"
    if env_path.exists() or env_path.is_symlink():
        raise RecoveryError("restore refuses to overwrite an existing .env; use a fresh deployment checkout")
    recovery_dir = target / "recovery"
    if recovery_dir.exists() or recovery_dir.is_symlink():
        raise RecoveryError("restore refuses to overwrite an existing recovery directory")
    tls_dir = target / "tls"
    if tls_dir.is_symlink() or (tls_dir.exists() and (not tls_dir.is_dir() or any(tls_dir.iterdir()))):
        raise RecoveryError("restore needs an absent or empty TLS directory; existing TLS files were preserved")
    validate_project_name(args.project_name)
    require_fresh_compose_project(args.project_name)
    for name, port in (("API", args.api_port), ("MCP", args.mcp_port)):
        if not 1 <= port <= 65535:
            raise RecoveryError(f"{name} restore port must be between 1 and 65535")
    if args.api_port == args.mcp_port:
        raise RecoveryError("API and MCP recovery ports must be different")
    require_free_loopback_port(args.api_port, "API")
    require_free_loopback_port(args.mcp_port, "MCP")
    project_root = target.parent.parent
    work, payload = decrypt_archive(archive, identity, staging)
    try:
        manifest = load_and_validate_payload(payload)
        if git_revision(project_root) != manifest["source_revision"]:
            raise RecoveryError("target checkout must be clean and match the exact source revision recorded in the backup")
        image = run(["docker", "image", "inspect", "--format", '{{.Id}}|{{index .Config.Labels "org.opencontainers.image.revision"}}', manifest["app_image_reference"]], timeout=60)
        if image.returncode or "|" not in image.stdout:
            raise RecoveryError("pull or build the exact candidate image, then retry restore preparation")
        image_id, image_revision = image.stdout.strip().split("|", 1)
        if image_id != manifest["app_image_id"] or image_revision != manifest["source_revision"]:
            raise RecoveryError("local candidate image ID or source revision label differs from the backup manifest")
        original_env = (payload / "deployment.env").read_text(encoding="utf-8")
        restored_values = parse_env(payload / "deployment.env")
        restored_image_ref = restored_values.get("DATA_FABRIC_IMAGE", "data-fabric:local").strip() or "data-fabric:local"
        if restored_image_ref != manifest["app_image_reference"]:
            raise RecoveryError("backup image reference does not match its protected configuration")
        if restored_values.get("DATA_FABRIC_SOURCE_REVISION", "").strip() != manifest["source_revision"]:
            raise RecoveryError("backup source revision does not match its protected configuration")
        configured = replace_env_values(original_env, {
            "API_PORT": str(args.api_port),
            "MCP_HTTPS_PORT": str(args.mcp_port),
            "DATA_FABRIC_TLS_GID": str(os.getgid()),
        })
        # The target and payload are checked above; exclusive creation prevents a race from replacing user files.
        recovery_dir.mkdir(mode=0o700)
        try:
            fd = os.open(env_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as stream:
                stream.write(configured)
                stream.flush()
                os.fsync(stream.fileno())
            dump_path = recovery_dir / "metadata.sql"
            fd = os.open(dump_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with (payload / "metadata.sql").open("rb") as source, os.fdopen(fd, "wb") as output:
                shutil.copyfileobj(source, output, length=1024 * 1024)
            os.chmod(env_path, 0o600)
        except Exception:
            env_path.unlink(missing_ok=True)
            shutil.rmtree(recovery_dir, ignore_errors=True)
            raise
        print(f"Restore payload prepared in {target}; no container was started and no database was overwritten.")
        print(f"Use unique Compose project {args.project_name}; loopback API/MCP ports {args.api_port}/{args.mcp_port}.")
        print(f"The archive records candidate source {manifest['source_revision']} and image {manifest['app_image_reference']} ({manifest['app_image_id']}).")
        print("Next: run onboard.py init to create a new local TLS pair, start metadata-db under that project name,")
        print("import recovery/metadata.sql, start the remaining services, run onboard.py doctor, then perform")
        print("one allowed and one denied read-only task with separate same-tenant and outsider identities.")
        print("Use docker-compose.recovery.yml; add docker-compose.release.yml only for the digest-pinned registry image.")
        print("Review docs/operations/mcp-profile-backup-recovery.md before starting the fresh profile.")
        return 0
    finally:
        shutil.rmtree(work, ignore_errors=True)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Encrypted backup and fresh-checkout restore preparation for the self-hosted MCP profile.")
    sub = parser.add_subparsers(dest="command", required=True)
    backup_parser = sub.add_parser("backup", help="quiesce the local API/MCP writers and create an age-encrypted profile backup")
    backup_parser.add_argument("--directory", type=Path, default=DEFAULT_DEPLOY_DIR)
    backup_parser.add_argument("--project-name", default="data-fabric-mcp")
    backup_parser.add_argument("--recipient", required=True, help="age public recipient; never a private identity")
    backup_parser.add_argument("--output", type=Path, required=True, help="new .age file; existing files are never overwritten")
    backup_parser.add_argument("--staging-dir", type=Path, required=True, help="owner-only (0700), encrypted or memory-backed temporary storage")
    backup_parser.set_defaults(handler=backup)
    for command, help_text in (("verify", "decrypt and verify a backup without displaying secret values"),
                               ("restore", "prepare a verified backup in a fresh matching checkout without starting services")):
        item = sub.add_parser(command, help=help_text)
        item.add_argument("--archive", type=Path, required=True)
        item.add_argument("--identity-file", type=Path, required=True, help="owner-only age private identity")
        item.add_argument("--staging-dir", type=Path, required=True, help="owner-only (0700), encrypted or memory-backed temporary storage")
        if command == "restore":
            item.add_argument("--target-deploy-dir", type=Path, required=True)
            item.add_argument("--project-name", required=True, help="unique fresh Compose project name")
            item.add_argument("--api-port", type=int, default=18080)
            item.add_argument("--mcp-port", type=int, default=18443)
            item.set_defaults(handler=restore)
        else:
            item.set_defaults(handler=verify)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        return args.handler(args)
    except RecoveryError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    except KeyboardInterrupt:
        print("error: interrupted; temporary plaintext files will be removed", file=sys.stderr)
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
