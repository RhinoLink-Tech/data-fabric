"""Regression tests for the self-hosted onboarding helpers."""
import base64
import json
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


if __name__ == "__main__":
    unittest.main()
