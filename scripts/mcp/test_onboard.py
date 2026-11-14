"""Regression tests for the self-hosted onboarding helpers."""
import base64
import json
import contextlib
import io
import os
import shutil
import stat
import subprocess
from unittest import mock
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

import onboard


class CredentialKeyringTests(unittest.TestCase):
    def valid_ring(self):
        encoded = base64.b64encode(bytes(range(32))).decode("ascii")
        return json.dumps({"key-1": encoded})

    def test_accepts_active_32_byte_key(self):
        ring, active = onboard.validate_credential_keyring(self.valid_ring(), "key-1")
        self.assertEqual(active, "key-1")
        self.assertEqual(len(base64.b64decode(ring[active])), 32)

    def test_creates_default_id_for_empty_keyring(self):
        ring, active = onboard.validate_credential_keyring("", "")
        self.assertIsNone(ring)
        self.assertEqual(active, "key-1")

    def test_infers_only_key_when_active_id_is_missing(self):
        ring, active = onboard.validate_credential_keyring(self.valid_ring(), "")
        self.assertEqual(active, "key-1")
        self.assertIn(active, ring)

    def test_rejects_malformed_json(self):
        with self.assertRaisesRegex(onboard.OnboardingError, "not valid JSON"):
            onboard.validate_credential_keyring("{broken", "key-1")

    def test_rejects_active_id_missing_from_existing_keyring(self):
        with self.assertRaisesRegex(onboard.OnboardingError, "does not contain its active ID"):
            onboard.validate_credential_keyring(self.valid_ring(), "key-2")

    def test_rejects_invalid_base64_or_wrong_key_length(self):
        for value in ("***", base64.b64encode(b"short").decode("ascii")):
            with self.subTest(value=value[:8]):
                ring = json.dumps({"key-1": value})
                with self.assertRaises(onboard.OnboardingError):
                    onboard.validate_credential_keyring(ring, "key-1")

    def test_rejects_ambiguous_multiple_keys_without_active_id(self):
        ring = json.dumps({"key-1": "AA==", "key-2": "AQ=="})
        with self.assertRaisesRegex(onboard.OnboardingError, "multiple keys"):
            onboard.validate_credential_keyring(ring, "")

    def test_init_preserves_invalid_existing_config_without_creating_tls(self):
        with tempfile.TemporaryDirectory() as directory:
            deploy = Path(directory)
            env = deploy / ".env"
            original = (
                "JWT_SECRET=change-me\n"
                "DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=key-1\n"
                "DATA_FABRIC_CREDENTIAL_KEYS={broken\n"
            )
            env.write_text(original, encoding="utf-8")
            env.chmod(0o600)

            with self.assertRaisesRegex(onboard.OnboardingError, "not valid JSON"):
                onboard.command_init(SimpleNamespace(directory=deploy))

            self.assertEqual(env.read_text(encoding="utf-8"), original)
            self.assertFalse((deploy / "tls").exists())


class OIDCIssuerDoctorTests(unittest.TestCase):
    def test_doctor_validates_oidc_issuer(self):
        cases = [
            ("valid_path", "https://id.example.test/issuer", True),
            ("query", "https://id.example.test/issuer?wrong=1", False),
            ("fragment", "https://id.example.test/issuer#fragment", False),
            ("userinfo", "https://user:synthetic-secret@id.example.test/issuer", False),
            ("bad_ipv6", "https://[invalid", False),
            ("bad_port", "https://id.example.test:invalid/issuer", False),
            ("bad_ipv6_port", "https://[::1]:oops/issuer", False),
            ("space_in_host", "https://id example.test/issuer", False),
            ("encoded_slash_in_host", "https://id%2Fexample.test/issuer", False),
            ("bad_path_escape", "https://id.example.test/%ZZ", False),
            ("ipvfuture", "https://[v1.a]/issuer", False),
            ("valid_ipv6_port", "https://[::1]:443/issuer", True),
            ("valid_ipv6_zone", "https://[fe80::1%25eth0]/issuer", True),
            ("valid_escaped_ipv6_zone", "https://[fe80::1%25eth%2Dtest]/issuer", True),
            ("valid_space_in_ipv6_zone", "https://[fe80::1%25eth%20test]/issuer", True),
            ("invalid_ipv6_zone_escape", "https://[fe80::1%25eth%2Ftest]/issuer", False),
            ("valid_escaped_path", "https://id.example.test/issuer%20name", True),
            ("http", "http://id.example.test/issuer", False),
        ]

        for name, issuer, expected_ok in cases:
            with self.subTest(case=name):
                with tempfile.TemporaryDirectory() as directory:
                    deploy = Path(directory)
                    env = deploy / ".env"
                    env.write_text(
                        f"MCP_OIDC_ISSUER={issuer}\n"
                        "MCP_OIDC_AUDIENCE=test-audience\n",
                        encoding="utf-8",
                    )

                    output = io.StringIO()
                    args = SimpleNamespace(
                        directory=deploy,
                        probe_oidc=False,
                    )

                    with (
                        contextlib.redirect_stdout(output),
                        mock.patch.object(
                            onboard, "compose_command",
                            return_value=SimpleNamespace(returncode=1),
                        ),
                        mock.patch.object(onboard, "check_identity"),
                        mock.patch.object(onboard, "source_connection_check"),
                    ):
                        onboard.command_doctor(args)

                    result = output.getvalue()

                    if expected_ok:
                        self.assertIn(
                            "OK  MCP OIDC issuer uses HTTPS", result
                        )
                    else:
                        self.assertIn(
                            "ERR MCP_OIDC_ISSUER must be a valid HTTPS issuer URL",
                            result,
                        )

                    self.assertIn("Doctor completed:", result)
                    self.assertIn("MCP OIDC audience is configured", result)
                    self.assertNotIn("synthetic-secret", result)


    def test_invalid_issuer_skips_oidc_probe(self):
        with tempfile.TemporaryDirectory() as directory:
            deploy = Path(directory)
            (deploy / ".env").write_text(
                "MCP_OIDC_ISSUER=https://id.example.test:invalid/issuer\n"
                "MCP_OIDC_AUDIENCE=test-audience\n", encoding="utf-8"
            )
            output = io.StringIO()
            args = SimpleNamespace(directory=deploy, probe_oidc=True)
            with (
                contextlib.redirect_stdout(output),
                mock.patch.object(onboard, "compose_command", return_value=SimpleNamespace(returncode=1)),
                mock.patch.object(onboard, "check_identity"),
                mock.patch.object(onboard, "source_connection_check"),
                mock.patch.object(onboard, "probe_oidc") as probe,
            ):
                onboard.command_doctor(args)
            probe.assert_not_called()
            self.assertIn("OIDC discovery was not probed because the issuer URL is invalid", output.getvalue())
            self.assertIn("Doctor completed:", output.getvalue())


@unittest.skipUnless(shutil.which("openssl"), "openssl is required for synthetic TLS fixtures")
class TLSPairTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.deploy = self.directory / "deploy"
        self.deploy.mkdir()
        onboard.init_tls(self.deploy, os.getgid())
        self.cert = self.deploy / "tls" / "tls.crt"
        self.key = self.deploy / "tls" / "tls.key"

    def snapshot(self):
        return [(path.read_bytes(), stat.S_IMODE(path.stat().st_mode)) for path in (self.cert, self.key)]

    def mismatch(self):
        other = self.directory / "other"
        other.mkdir()
        onboard.init_tls(other, os.getgid())
        self.key.write_bytes((other / "tls" / "tls.key").read_bytes())

    def doctor_output(self):
        output = io.StringIO()
        args = SimpleNamespace(directory=self.deploy, probe_oidc=False)
        with contextlib.redirect_stdout(output), mock.patch.object(onboard, "check_identity"):
            onboard.command_doctor(args)
        return output.getvalue()

    def test_matching_pair_is_preserved_and_passes_doctor(self):
        before = self.snapshot()
        self.assertEqual(onboard.init_tls(self.deploy, os.getgid()), "kept existing TLS certificate and key")
        self.assertIn("TLS certificate and private key are present, parseable and match", self.doctor_output())
        self.assertEqual(self.snapshot(), before)

    def test_mismatched_pair_is_rejected_without_modifying_files(self):
        self.mismatch()
        before = self.snapshot()
        with self.assertRaisesRegex(onboard.OnboardingError, "do not match") as error:
            onboard.init_tls(self.deploy, os.getgid())
        output = self.doctor_output()
        self.assertIn("TLS certificate and private key do not match", output)
        self.assertNotIn("present, parseable and match", output)
        self.assertNotIn("BEGIN PRIVATE KEY", str(error.exception) + output)
        self.assertEqual(self.snapshot(), before)

    def test_doctor_reports_valid_key_permissions_when_pair_mismatches(self):
        self.mismatch()
        env = self.deploy / ".env"
        env.write_text(f"DATA_FABRIC_TLS_GID={os.getgid()}\n", encoding="utf-8")
        env.chmod(0o600)
        with mock.patch.object(onboard, "compose_command", return_value=SimpleNamespace(returncode=1)):
            output = self.doctor_output()
        self.assertIn("TLS certificate and private key do not match", output)
        self.assertIn("OK  TLS key group/mode allow gateway read access without world access", output)
        self.assertNotIn("TLS key must be group-readable", output)

    def test_invalid_key_is_rejected_without_exposing_contents(self):
        self.key.write_text("invalid-private-key-material")
        with self.assertRaises(onboard.OnboardingError) as error:
            onboard.init_tls(self.deploy, os.getgid())
        self.assertNotIn("invalid-private-key-material", str(error.exception))
        self.assertIn("TLS files are missing or invalid", self.doctor_output())

    def test_matching_ec_pair_is_accepted(self):
        subprocess.run(["openssl", "req", "-x509", "-newkey", "ec",
                        "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-days", "1",
                        "-keyout", str(self.key), "-out", str(self.cert),
                        "-subj", "/CN=localhost"], check=True, capture_output=True, timeout=10)
        before = self.snapshot()
        onboard.init_tls(self.deploy, os.getgid())
        self.assertIn("present, parseable and match", self.doctor_output())
        self.assertEqual(self.snapshot(), before)

    def test_encrypted_key_fails_without_prompting(self):
        encrypted = self.directory / "encrypted.key"
        subprocess.run(["openssl", "pkey", "-in", str(self.key), "-aes-256-cbc",
                        "-passout", "pass:synthetic-test-password", "-out", str(encrypted)],
                       check=True, capture_output=True, timeout=10)
        self.key.write_bytes(encrypted.read_bytes())
        before = self.snapshot()
        with self.assertRaises(onboard.OnboardingError):
            onboard.init_tls(self.deploy, os.getgid())
        self.assertEqual(self.snapshot(), before)

    def test_doctor_preserves_certificate_expiry_check(self):
        original_run = subprocess.run
        def expired_certificate(command, **kwargs):
            if "-checkend" in command:
                return subprocess.CompletedProcess(command, 1)
            return original_run(command, **kwargs)
        with mock.patch.object(onboard.subprocess, "run", side_effect=expired_certificate):
            output = self.doctor_output()
        self.assertIn("TLS files are missing or invalid", output)
        self.assertNotIn("present, parseable and match", output)


class SelectedSourceResultTests(unittest.TestCase):
    def response(self, *, success=True, status="healthy"):
        return json.dumps({"success": success, "data": {"status": status}}).encode()

    def test_http_200_and_healthy_status_is_success(self):
        ok, message = onboard.selected_source_result(200, self.response())
        self.assertTrue(ok)
        self.assertEqual(message, "selected-source connection succeeded")

    def test_http_200_and_unhealthy_status_is_failure(self):
        ok, message = onboard.selected_source_result(200, self.response(status="unhealthy"))
        self.assertFalse(ok)
        self.assertIn("health state unhealthy", message)
        self.assertIn("host, port, database and credentials", message)

    def test_unhealthy_envelope_is_failure_even_if_health_state_says_healthy(self):
        ok, message = onboard.selected_source_result(200, self.response(success=False))
        self.assertFalse(ok)
        self.assertIn("no healthy result", message)

    def test_http_error_is_failure(self):
        ok, message = onboard.selected_source_result(400, b"")
        self.assertFalse(ok)
        self.assertIn("HTTP 400", message)

    def test_malformed_or_oversized_result_is_not_success(self):
        ok, message = onboard.selected_source_result(200, b"not-json")
        self.assertFalse(ok)
        self.assertIn("no healthy result", message)
        ok, message = onboard.selected_source_result(200, b" " * 1_048_577)
        self.assertFalse(ok)
        self.assertIn("no healthy result", message)

    def test_non_utf8_source_config_is_reported_without_exposing_contents(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "source.json"
            source.write_bytes(b'\xff{"password":"synthetic-secret"}')
            source.chmod(0o600)
            results = []
            output = io.StringIO()
            args = SimpleNamespace(source_config=str(source), source_type="mysql",
                                   management_token_env="DATA_FABRIC_TEST_TOKEN_UNSET")
            with contextlib.redirect_stdout(output):
                onboard.source_connection_check(args, Path(directory), Path(directory) / ".env", {}, results)
        self.assertEqual(results, [(False, "source configuration could not be read safely; protect it with chmod 600 and verify its JSON")])
        self.assertNotIn("synthetic-secret", output.getvalue())

    def test_non_ascii_source_config_is_sent_as_utf8_within_limit(self):
        config = {"password": "é" * 200_000}
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "source.json"
            source.write_text(json.dumps(config, ensure_ascii=False), encoding="utf-8")
            source.chmod(0o600)

            class Response:
                status = 200

                def __enter__(self):
                    return self

                def __exit__(self, *exc):
                    return False

                def read(self, *_):
                    return json.dumps({"success": True, "data": {"status": "healthy"}}).encode()

            sent = {}

            def fake_urlopen(request, timeout=None):
                sent["body"] = request.data
                return Response()

            args = SimpleNamespace(source_config=str(source), source_type="mysql",
                                   management_token_env="DATA_FABRIC_TEST_TOKEN")
            results = []
            with mock.patch.dict(os.environ, {"DATA_FABRIC_TEST_TOKEN": "synthetic-token"}), \
                    mock.patch.object(onboard.urllib.request, "urlopen", fake_urlopen), \
                    contextlib.redirect_stdout(io.StringIO()):
                onboard.source_connection_check(args, Path(directory), Path(directory) / ".env", {}, results)
        self.assertLessEqual(len(sent["body"]), 1_048_576)
        self.assertEqual(results[0][0], True)


class SourceRequestBoundaryTests(unittest.TestCase):
    def check_source(self, raw, *, from_stdin, status=200):
        output, results, sent = io.StringIO(), [], []

        class Response:
            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def read(self, _limit):
                return b'{"success":true,"data":{"status":"healthy"}}'

        def send(request, timeout):
            sent.append(request.data)
            if status == 413:
                error = onboard.urllib.error.HTTPError(request.full_url, 413, "too large", {}, io.BytesIO())
                self.addCleanup(error.close)
                raise error
            response = Response()
            response.status = status
            return response

        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "source.json"
            source.write_bytes(raw)
            source.chmod(0o600)
            args = SimpleNamespace(source_config="-" if from_stdin else str(source), source_type="mysql",
                                   management_token_env="DATA_FABRIC_TEST_TOKEN")
            stdin = SimpleNamespace(buffer=io.BytesIO(raw))
            with mock.patch.object(onboard.sys, "stdin", stdin), \
                    mock.patch.dict(os.environ, {"DATA_FABRIC_TEST_TOKEN": "synthetic-token"}), \
                    mock.patch.object(onboard.urllib.request, "urlopen", side_effect=send), \
                    contextlib.redirect_stdout(output):
                onboard.source_connection_check(args, Path(directory), Path(directory) / ".env", {}, results)
        return results, output.getvalue(), sent

    def test_utf8_stdin_and_file_limit_is_in_bytes(self):
        limit = onboard.MANAGEMENT_API_BODY_LIMIT
        prefix, suffix = b'{"host":"', b'"}'
        remaining = limit - len(prefix) - len(suffix)
        exact = prefix + ("é" * (remaining // 2)).encode("utf-8") + b"a" * (remaining % 2) + suffix
        self.assertEqual(len(exact), limit)
        for from_stdin in (True, False):
            with self.subTest(from_stdin=from_stdin, size="exact"):
                results, _, sent = self.check_source(exact, from_stdin=from_stdin)
                self.assertTrue(results[0][0])
                self.assertEqual(len(sent[0]), limit)
            with self.subTest(from_stdin=from_stdin, size="over"):
                results, output, sent = self.check_source(exact[:-len(suffix)] + b"a" + suffix, from_stdin=from_stdin)
                self.assertFalse(results[0][0])
                self.assertEqual(sent, [])
                self.assertIn("1 MiB", output)

    def test_outgoing_body_limit_uses_encoded_bytes(self):
        limit = onboard.MANAGEMENT_API_BODY_LIMIT
        self.assertEqual(len(onboard.source_request_body({"x": "a" * (limit - 8)})), limit)
        with self.assertRaisesRegex(onboard.OnboardingError, "limit"):
            onboard.source_request_body({"x": "a" * (limit - 7)})

    def test_http_413_is_actionable_and_redacts_configuration(self):
        results, output, sent = self.check_source(b'{"password":"synthetic-secret"}', from_stdin=True, status=413)
        self.assertFalse(results[0][0])
        self.assertEqual(len(sent), 1)
        self.assertIn("HTTP 413", output)
        self.assertIn("request-body limit", output)
        self.assertNotIn("synthetic-secret", output)
        self.assertNotIn("synthetic-token", output)

    def test_unpaired_surrogate_is_reported_without_traceback_or_secret(self):
        raw = b'{"password":"synthetic-secret", "host":"\\ud800"}'
        for from_stdin in (True, False):
            with self.subTest(from_stdin=from_stdin):
                results, output, sent = self.check_source(raw, from_stdin=from_stdin)
                self.assertFalse(results[0][0])
                self.assertEqual(sent, [])
                self.assertIn("Unicode", output)
                self.assertNotIn("synthetic-secret", output)
                self.assertNotIn("synthetic-token", output)


class NonUtf8EnvTests(unittest.TestCase):
    def make_deploy(self):
        directory = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, directory, True)
        env = directory / ".env"
        env.write_bytes(b"JWT_SECRET=synthetic-secret" + bytes([0xFF]) + bytes([10]))
        env.chmod(0o600)
        return directory, env

    def test_doctor_reports_encoding_failure_without_exposing_contents(self):
        directory, env = self.make_deploy()
        before = env.read_bytes()
        output = io.StringIO()
        with contextlib.redirect_stdout(output), mock.patch.object(onboard, "check_identity"):
            code = onboard.command_doctor(SimpleNamespace(directory=directory, probe_oidc=False))
        printed = output.getvalue()
        self.assertEqual(code, 1)
        self.assertIn("not valid UTF-8", printed)
        self.assertNotIn("synthetic-secret", printed)
        self.assertEqual(env.read_bytes(), before)

    def test_init_fails_cleanly_and_preserves_files(self):
        directory, env = self.make_deploy()
        before = env.read_bytes()
        with self.assertRaisesRegex(onboard.OnboardingError, "not valid UTF-8"):
            onboard.command_init(SimpleNamespace(directory=directory))
        self.assertEqual(env.read_bytes(), before)
        self.assertFalse((directory / "tls").exists())

    def test_token_fails_cleanly(self):
        directory, env = self.make_deploy()
        before = env.read_bytes()
        args = SimpleNamespace(directory=directory, user="synthetic-user", tenant="quickstart",
                               username=None, ttl_minutes=15, admin=False)
        with mock.patch.object(onboard.stat, "S_IMODE", return_value=0o600):
            with self.assertRaisesRegex(onboard.OnboardingError, "not valid UTF-8"):
                onboard.command_token(args)
        self.assertEqual(env.read_bytes(), before)


if __name__ == "__main__":
    unittest.main()
