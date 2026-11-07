#!/usr/bin/env python3
"""Safe bootstrap and local diagnostics for the self-hosted MCP profile."""
import argparse
import base64
import binascii
import hashlib
import hmac
import json
import os
import re
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_DEPLOY_DIR = REPO_ROOT / "deploy" / "mcp"
SECRET_KEYS = ("JWT_SECRET", "METADATA_DB_ROOT_PASSWORD", "METADATA_DB_PASSWORD", "SAMPLE_SOURCE_ROOT_PASSWORD", "SAMPLE_READER_PASSWORD")
PLACEHOLDERS = {"change-me", "changeme", "replace-me", "replace_me", "todo"}

class OnboardingError(Exception):
    pass


def parse_env(path):
    values, duplicates = {}, set()
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
                except json.JSONDecodeError:
                    value = value[1:-1]
            else:
                value = value[1:-1]
        if key in values:
            duplicates.add(key)
        values[key] = value
    if duplicates:
        raise OnboardingError(".env contains duplicate keys: " + ", ".join(sorted(duplicates)))
    return values


def is_placeholder(value):
    normalized = value.strip().lower()
    return not normalized or normalized in PLACEHOLDERS or (normalized.startswith("<") and normalized.endswith(">"))


def replace_env_values(text, replacements):
    lines, seen, output = text.splitlines(), set(), []
    for line in lines:
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


def atomic_private_write(path, content):
    if path.is_symlink():
        raise OnboardingError(f"refusing to write through a symlink: {path.name}")
    fd, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp_name, path)
        os.chmod(path, 0o600)
        dir_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(dir_fd)
        finally:
            os.close(dir_fd)
    except Exception:
        try:
            os.unlink(temp_name)
        except FileNotFoundError:
            pass
        raise


def env_paths(directory):
    directory = directory.expanduser().resolve()
    return directory, directory / ".env", directory / ".env.example"


def init_tls(directory, group_id):
    tls_dir = directory / "tls"
    tls_dir.mkdir(mode=0o750, parents=True, exist_ok=True)
    if tls_dir.is_symlink():
        raise OnboardingError("refusing to use a symlink for the tls directory")
    cert, key = tls_dir / "tls.crt", tls_dir / "tls.key"
    if cert.exists() or key.exists() or cert.is_symlink() or key.is_symlink():
        if cert.exists() and key.exists() and not cert.is_symlink() and not key.is_symlink():
            return "kept existing TLS certificate and key"
        raise OnboardingError("tls/tls.crt and tls/tls.key must both exist or both be absent; existing files were left untouched")
    if group_id != os.getgid():
        try:
            os.chown(tls_dir, -1, group_id)
        except OSError as exc:
            raise OnboardingError(f"cannot assign tls directory to group {group_id}; set DATA_FABRIC_TLS_GID to your current group or prepare TLS files manually") from exc
    os.chmod(tls_dir, 0o750)
    openssl = shutil.which("openssl")
    if not openssl:
        raise OnboardingError("openssl is required to create the local TLS certificate")
    temp_dir = Path(tempfile.mkdtemp(prefix=".tls-init-", dir=tls_dir))
    temp_cert, temp_key = temp_dir / "tls.crt", temp_dir / "tls.key"
    command = [openssl, "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "365", "-keyout", str(temp_key), "-out", str(temp_cert), "-subj", "/CN=data-fabric.local", "-addext", "subjectAltName=DNS:data-fabric.local,DNS:localhost,IP:127.0.0.1"]
    try:
        result = subprocess.run(command, cwd=directory, capture_output=True, text=True, timeout=30)
        if result.returncode:
            raise OnboardingError("openssl could not create the local TLS certificate; check the openssl installation")
        if group_id != os.getgid():
            os.chown(temp_dir, -1, group_id)
            os.chown(temp_key, -1, group_id)
            os.chown(temp_cert, -1, group_id)
        os.chmod(temp_dir, 0o750)
        os.chmod(temp_key, 0o640)
        os.chmod(temp_cert, 0o644)
        os.replace(temp_cert, cert)
        os.replace(temp_key, key)
        return "created localhost TLS files; the private key is group-readable only by DATA_FABRIC_TLS_GID"
    except OSError as exc:
        raise OnboardingError("could not safely install TLS files; generated temporary files were removed") from exc
    finally:
        shutil.rmtree(temp_dir, ignore_errors=True)


def command_init(args):
    directory, env_path, example_path = env_paths(args.directory)
    if not directory.is_dir():
        raise OnboardingError(f"deployment directory does not exist: {directory}")
    if env_path.is_symlink():
        raise OnboardingError("refusing to read or write .env through a symlink")
    if env_path.exists():
        if not env_path.is_file():
            raise OnboardingError(".env must be a regular file")
        text, current = env_path.read_text(encoding="utf-8"), parse_env(env_path)
    else:
        if not example_path.is_file():
            raise OnboardingError(".env.example is missing; restore the deployment template before initializing")
        text, current = example_path.read_text(encoding="utf-8"), parse_env(example_path)
    replacements = {}
    for key in SECRET_KEYS:
        if is_placeholder(current.get(key, "")):
            replacements[key] = secrets.token_urlsafe(32)
    ring_value = current.get("DATA_FABRIC_CREDENTIAL_KEYS", "").strip()
    active_id = current.get("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID", "").strip()
    if is_placeholder(active_id):
        if is_placeholder(ring_value):
            active_id = "key-1"
        else:
            try:
                existing_ring = json.loads(ring_value)
            except json.JSONDecodeError as exc:
                raise OnboardingError("existing credential keyring is not valid JSON; fix it manually before initializing") from exc
            if not isinstance(existing_ring, dict) or len(existing_ring) != 1:
                raise OnboardingError("existing keyring has no active ID and multiple keys; set DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID manually")
            active_id = next(iter(existing_ring))
        replacements["DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID"] = active_id
    if not active_id or ":" in active_id:
        raise OnboardingError("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID must be non-empty and must not contain ':'")
    if is_placeholder(ring_value):
        replacements["DATA_FABRIC_CREDENTIAL_KEYS"] = json.dumps({active_id: base64.b64encode(secrets.token_bytes(32)).decode("ascii")}, separators=(",", ":"))
    tls_gid = current.get("DATA_FABRIC_TLS_GID", "").strip()
    if is_placeholder(tls_gid):
        tls_gid = str(os.getgid())
        replacements["DATA_FABRIC_TLS_GID"] = tls_gid
    try:
        group_id = int(tls_gid)
        if group_id < 0:
            raise ValueError
    except ValueError as exc:
        raise OnboardingError("DATA_FABRIC_TLS_GID must be a non-negative numeric group ID") from exc
    if not env_path.exists() or replacements:
        atomic_private_write(env_path, replace_env_values(text, replacements))
    else:
        os.chmod(env_path, 0o600)
    tls_result = init_tls(directory, group_id)
    print("Initialized the current MySQL + remote MCP profile.")
    print("Generated or filled: " + (", ".join(sorted(replacements)) if replacements else "no missing environment values"))
    print(f"Protected configuration: {env_path} (mode 0600)")
    print(tls_result + ".")
    print("Next: fill MCP_OIDC_ISSUER and MCP_OIDC_AUDIENCE in .env, then run doctor.")
    print("Existing non-empty secrets, identity settings, database files and TLS files are preserved.")
    return 0

def add_result(results, ok, message):
    results.append((bool(ok), message))
    print(("OK  " if ok else "ERR ") + message)


def secure_json_file(path):
    path = path.expanduser()
    if path.is_symlink() or not path.is_file():
        raise OnboardingError("source configuration must be a regular file, not a symlink")
    if path.stat().st_size > 1_048_576:
        raise OnboardingError("source configuration exceeds the 1 MiB management API limit")
    if stat.S_IMODE(path.stat().st_mode) & 0o077:
        raise OnboardingError("source configuration file must be readable only by its owner (chmod 600)")
    return json.loads(path.read_text(encoding="utf-8"))


def decode_jwt_payload(token):
    parts = token.strip().split(".")
    if len(parts) != 3:
        raise ValueError("token is not a compact JWT")
    payload = parts[1] + "=" * (-len(parts[1]) % 4)
    result = json.loads(base64.urlsafe_b64decode(payload.encode("ascii")))
    if not isinstance(result, dict):
        raise ValueError("JWT payload is not an object")
    return result


def claim_path(payload, path):
    current = payload
    for item in path.split("."):
        if not isinstance(current, dict) or item not in current:
            return None
        current = current[item]
    return current


def check_identity(args, values, results):
    if not args.api_user:
        return
    token = os.environ.get(args.token_env, "")
    if not token:
        add_result(results, False, f"identity alignment could not read token from environment variable {args.token_env}")
        return
    try:
        claims = decode_jwt_payload(token)
    except (ValueError, UnicodeError, json.JSONDecodeError, binascii.Error):
        add_result(results, False, "identity alignment could not inspect the supplied compact JWT")
        return
    # This is a claim-shape hint only. The gateway remains responsible for signature validation.
    audience = claims.get("aud")
    audiences = audience if isinstance(audience, list) else [audience]
    tenant_name = values.get("MCP_OIDC_TENANT_CLAIM", "tenant").strip() or "tenant"
    scope_name = values.get("MCP_OIDC_SCOPE_CLAIM", "scope").strip() or "scope"
    scope_value = claim_path(claims, scope_name)
    scopes = set(scope_value.split()) if isinstance(scope_value, str) else {item for item in scope_value if isinstance(item, str)} if isinstance(scope_value, list) else set()
    required = set((values.get("MCP_OIDC_REQUIRED_SCOPES", "data-fabric.query").strip() or "data-fabric.query").split())
    now = int(time.time())
    ok = (
        claims.get("sub") == args.api_user
        and (args.tenant is None or claim_path(claims, tenant_name) == args.tenant)
        and claims.get("iss") == values.get("MCP_OIDC_ISSUER", "")
        and values.get("MCP_OIDC_AUDIENCE", "") in audiences
        and required.issubset(scopes)
        and isinstance(claims.get("exp"), (int, float)) and claims["exp"] > now
        and (not isinstance(claims.get("nbf"), (int, float)) or claims["nbf"] <= now)
    )
    add_result(results, ok, "unverified token principal/tenant/issuer/audience/scope claims " + ("match the selected profile" if ok else "do not match the selected profile"))
    print("    Advisory only: the MCP gateway validates the signed token on every request.")


def probe_oidc(issuer, timeout=5):
    issuer = issuer.rstrip("/")
    request = urllib.request.Request(issuer + "/.well-known/openid-configuration", headers={"Accept": "application/json", "User-Agent": "data-fabric-onboard/1"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        if response.status != 200 or not response.geturl().startswith("https://"):
            raise ValueError("OIDC discovery did not return HTTPS 200")
        document = json.loads(response.read(1024 * 1024))
    discovered_issuer = document.get("issuer") if isinstance(document, dict) else None
    if not isinstance(discovered_issuer, str) or discovered_issuer.rstrip("/") != issuer:
        raise ValueError("discovery issuer does not match configured issuer")
    jwks_url = document.get("jwks_uri")
    if not isinstance(jwks_url, str):
        raise ValueError("discovery metadata has no JWKS URL")
    jwks = urllib.parse.urlparse(jwks_url)
    if jwks.scheme != "https" or not jwks.netloc:
        raise ValueError("discovery JWKS URL must use HTTPS")
    jwks_request = urllib.request.Request(jwks_url, headers={"Accept": "application/json", "User-Agent": "data-fabric-onboard/1"})
    with urllib.request.urlopen(jwks_request, timeout=timeout) as response:
        if response.status != 200 or not response.geturl().startswith("https://"):
            raise ValueError("JWKS endpoint did not return HTTPS 200")
        keys = json.loads(response.read(1024 * 1024))
    if not isinstance(keys, dict) or not isinstance(keys.get("keys"), list) or not keys["keys"]:
        raise ValueError("JWKS response does not contain a non-empty keys array")


def compose_command(directory, env_path, *args):
    command = ["docker", "compose", "--env-file", str(env_path), "-f", "docker-compose.yml", *args]
    env = os.environ.copy()
    keys = SECRET_KEYS + ("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID", "DATA_FABRIC_CREDENTIAL_KEYS", "MCP_OIDC_ISSUER", "MCP_OIDC_AUDIENCE", "MCP_OIDC_REQUIRED_SCOPES", "MCP_OIDC_SCOPE_CLAIM", "MCP_OIDC_TENANT_CLAIM", "DATA_FABRIC_TLS_GID", "API_PORT", "MCP_HTTPS_PORT")
    for key in keys:
        env.pop(key, None)
    return subprocess.run(command, cwd=directory, env=env, capture_output=True, text=True, timeout=15)


def docker_status(directory, env_path, results):
    try:
        result = compose_command(directory, env_path, "ps", "--format", "json")
    except (OSError, subprocess.TimeoutExpired):
        add_result(results, False, "Docker Compose status could not be read; confirm Docker Engine and Compose are available")
        return set()
    if result.returncode:
        add_result(results, False, "Docker Compose status could not be read; run docker compose ps for local diagnostics")
        return set()
    output = result.stdout.strip()
    if not output:
        add_result(results, False, "the current Compose profile has no running services; start it with docker compose up -d")
        return set()
    try:
        data = json.loads(output)
        items = data if isinstance(data, list) else [data]
    except json.JSONDecodeError:
        try:
            items = [json.loads(line) for line in output.splitlines() if line.strip()]
        except json.JSONDecodeError:
            add_result(results, False, "Docker Compose returned an unrecognized status format; upgrade Compose and retry")
            return set()
    expected = {"metadata-db", "sample-source", "data-fabric-api", "data-fabric-mcp"}
    seen, services_ok = set(), True
    for item in items:
        service = item.get("Service") or item.get("Name")
        state = str(item.get("State") or "").lower()
        status = str(item.get("Status") or "").lower()
        health = str(item.get("Health") or "").lower()
        running = state == "running" or status.startswith("up ")
        healthy = health == "healthy" or "(healthy)" in status
        if service:
            seen.add(service)
            if not running or not healthy or "unhealthy" in status or "starting" in status:
                services_ok = False
    missing = expected - seen
    services_ok = services_ok and not missing
    if missing:
        add_result(results, False, "missing Compose services: " + ", ".join(sorted(missing)) + "; run docker compose up -d")
    elif services_ok:
        add_result(results, True, "metadata DB, sample source, API and remote MCP services are running and healthy")
    else:
        add_result(results, False, "one or more Compose services are not healthy; inspect docker compose ps and logs for the affected service")
    return seen if services_ok else set()


def local_http(url, timeout=5, context=None):
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=timeout, context=context) as response:
            return response.status
    except urllib.error.HTTPError as exc:
        return exc.code


def check_runtime(args, directory, env_path, values, results):
    if not docker_status(directory, env_path, results):
        return
    try:
        port = int(values.get("API_PORT", "8080").strip() or "8080")
        if not 1 <= port <= 65535:
            raise ValueError
        code = local_http(f"http://127.0.0.1:{port}/health")
        add_result(results, code == 200, "local management API health endpoint " + ("responds" if code == 200 else f"returned HTTP {code}"))
    except (ValueError, OSError, urllib.error.URLError, TimeoutError):
        add_result(results, False, "local management API health check failed; confirm API_PORT and inspect data-fabric-api logs")
    if not args.skip_mcp_tls:
        try:
            port = int(values.get("MCP_HTTPS_PORT", "8443").strip() or "8443")
            if not 1 <= port <= 65535:
                raise ValueError
            context = ssl.create_default_context(cafile=str(directory / "tls" / "tls.crt"))
            code = local_http(f"https://{args.mcp_host}:{port}/mcp", context=context)
            add_result(results, code == 401, "remote MCP TLS/auth probe " + ("returned the expected unauthenticated 401" if code == 401 else f"returned HTTP {code}, expected 401"))
        except (ValueError, OSError, ssl.SSLError, urllib.error.URLError, TimeoutError):
            add_result(results, False, "remote MCP TLS probe failed; check certificate SAN, key permissions, MCP_HTTPS_PORT and MCP logs")
    try:
        result = compose_command(directory, env_path, "exec", "-T", "data-fabric-api", "python3", "-c", "import sys; sys.path.insert(0, '/opt/data-fabric/python'); import sqlglot")
        add_result(results, result.returncode == 0, "packaged SQL translation parser " + ("is importable" if result.returncode == 0 else "is unavailable in the API image; rebuild or pull the selected image"))
    except (OSError, subprocess.TimeoutExpired):
        add_result(results, False, "could not check SQL translation parser in the API image")

def source_connection_check(args, directory, env_path, values, results):
    if not args.source_config:
        return
    if not args.source_type:
        add_result(results, False, "--source-type is required with --source-config")
        return
    if args.source_config == "-":
        try:
            raw = sys.stdin.read(1_048_577)
            if len(raw) > 1_048_576:
                raise ValueError("source configuration exceeds 1 MiB")
            config = json.loads(raw)
        except (json.JSONDecodeError, ValueError):
            add_result(results, False, "source configuration on stdin is not valid JSON or exceeds 1 MiB")
            return
    else:
        try:
            config = secure_json_file(Path(args.source_config))
        except (OSError, OnboardingError, json.JSONDecodeError):
            add_result(results, False, "source configuration could not be read safely; protect it with chmod 600 and verify its JSON")
            return
    if not isinstance(config, dict):
        add_result(results, False, "source configuration must be a JSON object")
        return
    token = os.environ.get(args.management_token_env, "")
    if not token:
        add_result(results, False, f"set {args.management_token_env} to a short-lived management token before testing a selected source")
        return
    if "\r" in token or "\n" in token:
        add_result(results, False, f"{args.management_token_env} contains an invalid line break; mint a new token")
        return
    try:
        port = int(values.get("API_PORT", "8080").strip() or "8080")
        if not 1 <= port <= 65535:
            raise ValueError
    except ValueError:
        add_result(results, False, "API_PORT must be a valid TCP port before testing a selected source")
        return
    request_id = str(uuid.uuid4())
    url = f"http://127.0.0.1:{port}/api/v1/database/test-connection?type={urllib.parse.quote(args.source_type, safe='')}"
    request = urllib.request.Request(url, data=json.dumps(config, separators=(",", ":")).encode("utf-8"), method="POST", headers={
        "Content-Type": "application/json", "Authorization": f"Bearer {token}", "X-Correlation-ID": request_id,
    })
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            code = response.status
    except urllib.error.HTTPError as exc:
        code = exc.code
    except (OSError, urllib.error.URLError, TimeoutError):
        add_result(results, False, f"selected-source request could not reach the loopback API; correlation ID {request_id}")
        return
    ok = 200 <= code < 300
    result = "selected-source connection succeeded" if ok else f"selected-source connection failed with HTTP {code}"
    add_result(results, ok, f"{result}; correlation ID {request_id}")


def command_doctor(args):
    directory, env_path, _ = env_paths(args.directory)
    results, values = [], {}
    if env_path.is_symlink() or not env_path.is_file():
        add_result(results, False, "deploy/mcp/.env is missing or not a regular file; run onboard.py init")
    else:
        try:
            values = parse_env(env_path)
            mode = stat.S_IMODE(env_path.stat().st_mode)
            add_result(results, mode == 0o600, ".env permissions are owner-only" if mode == 0o600 else ".env permissions must be 0600; run chmod 600 deploy/mcp/.env")
        except (OSError, OnboardingError) as exc:
            add_result(results, False, f".env could not be parsed safely: {exc}")
    for key in SECRET_KEYS:
        value = values.get(key, "")
        ok = not is_placeholder(value) and (key != "JWT_SECRET" or len(value.encode("utf-8")) >= 32)
        add_result(results, ok, f"{key} is configured" if ok else f"{key} is missing, a placeholder, or too short; run onboard.py init and review .env")
    active = values.get("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID", "").strip()
    try:
        keyring = json.loads(values.get("DATA_FABRIC_CREDENTIAL_KEYS", ""))
        if not isinstance(keyring, dict) or not active or active not in keyring:
            raise ValueError
        decoded = {name: base64.b64decode(encoded, validate=True) for name, encoded in keyring.items() if isinstance(encoded, str)}
        if len(decoded) != len(keyring) or any(len(key) != 32 for key in decoded.values()):
            raise ValueError
        add_result(results, True, "credential keyring is valid and its active AES-256 key is present")
    except (json.JSONDecodeError, ValueError, binascii.Error, TypeError):
        add_result(results, False, "credential keyring is invalid or lacks its active 32-byte key; do not replace an existing key without a verified backup")
    issuer = values.get("MCP_OIDC_ISSUER", "").strip()
    parsed_issuer = urllib.parse.urlparse(issuer)
    issuer_ok = parsed_issuer.scheme == "https" and bool(parsed_issuer.hostname)
    add_result(results, issuer_ok, "MCP OIDC issuer uses HTTPS" if issuer_ok else "MCP_OIDC_ISSUER must be the HTTPS issuer URL from your identity provider")
    audience = values.get("MCP_OIDC_AUDIENCE", "").strip()
    add_result(results, bool(audience), "MCP OIDC audience is configured" if audience else "set MCP_OIDC_AUDIENCE to the resource audience registered with your identity provider")
    scopes = (values.get("MCP_OIDC_REQUIRED_SCOPES", "data-fabric.query").strip() or "data-fabric.query").split()
    add_result(results, bool(scopes), "MCP required scopes are configured" if scopes else "configure MCP_OIDC_REQUIRED_SCOPES")
    tls_dir = directory / "tls"
    cert, key = tls_dir / "tls.crt", tls_dir / "tls.key"
    tls_files = cert.is_file() and key.is_file() and not tls_dir.is_symlink() and not cert.is_symlink() and not key.is_symlink()
    key_access = False
    if tls_files:
        try:
            gid = int(values.get("DATA_FABRIC_TLS_GID", "10001") or "10001")
            key_mode, key_gid = stat.S_IMODE(key.stat().st_mode), key.stat().st_gid
            dir_mode, dir_gid = stat.S_IMODE(tls_dir.stat().st_mode), tls_dir.stat().st_gid
            key_access = (
                key_gid == gid and dir_gid == gid
                and (key_mode & 0o007) == 0 and (key_mode & 0o020) == 0 and bool(key_mode & 0o040)
                and (dir_mode & 0o007) == 0 and (dir_mode & 0o020) == 0 and bool(dir_mode & 0o010)
            )
        except (OSError, ValueError):
            key_access = False
        openssl = shutil.which("openssl")
        try:
            cert_ok = bool(openssl and subprocess.run([openssl, "x509", "-in", str(cert), "-noout", "-checkend", "0"], capture_output=True, timeout=10).returncode == 0)
            key_ok = bool(openssl and subprocess.run([openssl, "pkey", "-in", str(key), "-noout"], capture_output=True, timeout=10).returncode == 0)
        except (OSError, subprocess.TimeoutExpired):
            cert_ok = key_ok = False
        tls_files = cert_ok and key_ok
    add_result(results, tls_files, "TLS certificate and private key are present and parseable" if tls_files else "TLS files are missing or invalid; run onboard.py init or replace both with a matching pair")
    add_result(results, tls_files and key_access, "TLS key group/mode allow gateway read access without world access" if tls_files and key_access else "TLS key must be group-readable by DATA_FABRIC_TLS_GID and not world-readable; use a restricted group and chmod 640")
    if args.probe_oidc:
        if not issuer_ok:
            add_result(results, False, "OIDC discovery was not probed because the issuer URL is invalid")
        else:
            try:
                probe_oidc(issuer)
                add_result(results, True, "OIDC discovery and HTTPS JWKS endpoint are reachable and match the configured issuer")
            except (OSError, TypeError, ValueError, urllib.error.URLError, TimeoutError, json.JSONDecodeError):
                add_result(results, False, "OIDC discovery failed; verify outbound HTTPS, issuer spelling, discovery metadata and JWKS certificate")
    check_identity(args, values, results)
    if values and env_path.is_file():
        try:
            compose = compose_command(directory, env_path, "config", "--quiet")
            compose_ok = compose.returncode == 0
        except (OSError, subprocess.TimeoutExpired):
            compose_ok = False
        add_result(results, compose_ok, "Docker Compose profile resolves without exposing environment values" if compose_ok else "Docker Compose configuration is invalid; run docker compose --env-file .env config and fix the named missing setting")
        if compose_ok:
            check_runtime(args, directory, env_path, values, results)
            source_connection_check(args, directory, env_path, values, results)
    failures = sum(not ok for ok, _ in results)
    print(f"Doctor completed: {len(results) - failures} passed, {failures} needs attention.")
    return 1 if failures else 0


def b64url(data):
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def command_token(args):
    _, env_path, _ = env_paths(args.directory)
    if env_path.is_symlink() or not env_path.is_file():
        raise OnboardingError("deploy/mcp/.env is missing; run onboard.py init first")
    if stat.S_IMODE(env_path.stat().st_mode) & 0o077:
        raise OnboardingError(".env must be owner-only (chmod 600) before minting a token")
    values = parse_env(env_path)
    secret = values.get("JWT_SECRET", "")
    if is_placeholder(secret) or len(secret.encode("utf-8")) < 32:
        raise OnboardingError("JWT_SECRET is missing or too short; run onboard.py init and doctor")
    user, tenant = args.user.strip(), args.tenant.strip()
    username = (args.username or user).strip()
    if not user or len(user) > 128 or not username or len(username) > 128 or len(tenant) > 128:
        raise OnboardingError("user, username and tenant must be non-empty (except tenant) and at most 128 characters")
    now = int(time.time())
    claims = {"userId": user, "tenantId": tenant, "username": username, "roles": ["admin"] if args.admin else [], "iss": "data-fabric", "sub": user, "iat": now, "nbf": now, "exp": now + args.ttl_minutes * 60}
    header = b64url(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode("utf-8"))
    payload = b64url(json.dumps(claims, separators=(",", ":")).encode("utf-8"))
    signing_input = f"{header}.{payload}".encode("ascii")
    signature = hmac.new(secret.encode("utf-8"), signing_input, hashlib.sha256).digest()
    print(f"{header}.{payload}.{b64url(signature)}")
    return 0


def build_parser():
    parser = argparse.ArgumentParser(description="Initialize and diagnose the self-hosted Data Fabric MCP profile.")
    sub = parser.add_subparsers(dest="command", required=True)
    init = sub.add_parser("init", help="create missing secrets and local TLS files without replacing existing values")
    init.add_argument("--directory", type=Path, default=DEFAULT_DEPLOY_DIR)
    init.set_defaults(handler=command_init)
    doctor = sub.add_parser("doctor", help="validate configuration and diagnose only this Compose profile")
    doctor.add_argument("--directory", type=Path, default=DEFAULT_DEPLOY_DIR)
    doctor.add_argument("--probe-oidc", action="store_true", help="make an HTTPS GET to the configured issuer discovery endpoint")
    doctor.add_argument("--api-user", help="expected API principal when inspecting unverified JWT claims")
    doctor.add_argument("--tenant", help="expected tenant claim for identity alignment")
    doctor.add_argument("--token-env", default="MCP_ACCESS_TOKEN", help="environment variable containing the provider JWT")
    doctor.add_argument("--mcp-host", default="localhost", help="host name on the MCP certificate")
    doctor.add_argument("--skip-mcp-tls", action="store_true", help="skip local HTTPS probe when a custom ingress terminates TLS")
    doctor.add_argument("--source-config", help="selected source JSON file (mode 0600) or - for stdin; sent only to loopback")
    doctor.add_argument("--source-type", help="driver type for the selected-source connectivity check")
    doctor.add_argument("--management-token-env", default="DF_MANAGEMENT_TOKEN", help="environment variable containing a short-lived API token")
    doctor.set_defaults(handler=command_doctor)
    token = sub.add_parser("token", help="mint a short-lived local management API JWT; stdout contains only the token")
    token.add_argument("--directory", type=Path, default=DEFAULT_DEPLOY_DIR)
    token.add_argument("--user", required=True, help="use the same principal as the MCP token sub")
    token.add_argument("--tenant", default="quickstart", help="shared tenant (default: quickstart)")
    token.add_argument("--username", help="display name (defaults to --user)")
    token.add_argument("--ttl-minutes", type=int, default=15, choices=range(1, 16), metavar="1..15")
    token.add_argument("--admin", action="store_true", help="add the admin role for explicitly requested admin operations")
    token.set_defaults(handler=command_token)
    return parser


def main():
    args = build_parser().parse_args()
    try:
        return args.handler(args)
    except OnboardingError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    except KeyboardInterrupt:
        print("error: interrupted; existing configuration was preserved", file=sys.stderr)
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
