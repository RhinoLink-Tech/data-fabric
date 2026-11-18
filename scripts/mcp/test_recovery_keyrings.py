"""Credential backup preflight contracts using synthetic container metadata."""
import base64
import json
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

import recovery


class CredentialPreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.env = self.directory / ".env"
        self.key1 = base64.b64encode(b"a" * 32).decode()
        self.key2 = base64.b64encode(b"b" * 32).decode()
        self.values = {"DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID": "key-a", "DATA_FABRIC_CREDENTIAL_KEYS": json.dumps({"key-a": self.key1, "old": self.key2})}
        self.running = list(recovery.APP_SERVICES)

    def inspect(self, api, mcp):
        def docker(args, **kwargs):
            service = args[-1]
            value = api if service == "api-id" else mcp
            if value is None:
                return SimpleNamespace(returncode=1, stdout="", stderr="redacted")
            return SimpleNamespace(returncode=0, stdout=json.dumps(value), stderr="")
        def compose(directory, env, project, *args):
            return SimpleNamespace(stdout="api-id" if args[-1] == recovery.APP_SERVICES[0] else "mcp-id")
        return mock.patch.object(recovery, "run", side_effect=docker), mock.patch.object(recovery, "compose_result", side_effect=compose)

    def entries(self, active=None, keys=None):
        return ["OTHER=value", "DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=" + (active or "key-a"),
                "DATA_FABRIC_CREDENTIAL_KEYS=" + (keys or self.values["DATA_FABRIC_CREDENTIAL_KEYS"])]

    def verify(self, api, mcp):
        run, compose = self.inspect(api, mcp)
        with run, compose:
            return recovery.verify_running_credential_keyrings(self.directory, self.env, "test", self.running, self.values)

    def test_accepts_equivalent_json_and_returns_container_ids(self):
        reordered = json.dumps({"old": self.key2, "key-a": self.key1}, indent=2)
        result = self.verify(self.entries(keys=reordered), self.entries())
        self.assertEqual(result, {recovery.APP_SERVICES[0]: "api-id", recovery.APP_SERVICES[1]: "mcp-id"})

    def test_rejects_overrides_disk_drift_missing_old_keys_and_two_container_disagreement(self):
        cases = [
            (self.entries(active="old"), self.entries()),
            (self.entries(keys=json.dumps({"key-a": self.key2, "old": self.key2})), self.entries()),
            (self.entries(keys=json.dumps({"key-a": self.key1})), self.entries()),
            (self.entries(), self.entries(active="old")),
        ]
        for api, mcp in cases:
            with self.subTest(api=api == self.entries()):
                with self.assertRaises(recovery.RecoveryError) as err:
                    self.verify(api, mcp)
                self.assertIn("differs", str(err.exception))
                self.assertNotIn(self.key1, str(err.exception))
                self.assertNotIn(self.key2, str(err.exception))

    def test_backup_mismatch_exits_before_stopping_or_publishing(self):
        (self.directory / "docker-compose.yml").write_text("services: {}\n")
        self.env.write_text("\n".join(f"{key}={value}" for key, value in self.values.items()) +
                            "\nDATA_FABRIC_SOURCE_REVISION=revision\nDATA_FABRIC_IMAGE=local-image\n")
        self.env.chmod(0o600)
        args = SimpleNamespace(directory=self.directory, project_name="test", output=self.directory.parent / (self.directory.name + ".age"),
                               recipient="age-fixture", staging_dir=self.directory / "staging", release_source_archive=None)
        calls = []
        def compose(directory, env, project, *args):
            calls.append(args)
            if args == ("ps", "--services", "--status", "running"):
                return SimpleNamespace(stdout="\n".join(recovery.APP_SERVICES))
            if args[-1] == "metadata-db":
                return SimpleNamespace(stdout="db-id")
            return SimpleNamespace(stdout="api-id" if args[-1] == recovery.APP_SERVICES[0] else "mcp-id")
        def run(args, **kwargs):
            if args[-1] == "db-id":
                return SimpleNamespace(returncode=0, stdout="sha256:db|mysql:fixture", stderr="")
            source = self.entries(active="old") if args[-1] == "mcp-id" else self.entries()
            return SimpleNamespace(returncode=0, stdout=json.dumps(source), stderr="")
        healthy = {service: {"State": "running", "Health": "healthy"} for service in (*recovery.APP_SERVICES, "metadata-db")}
        with mock.patch.object(recovery, "git_revision", return_value="revision"), \
             mock.patch.object(recovery.shutil, "which", return_value="/synthetic/age"), \
             mock.patch.object(recovery, "secure_staging_root", return_value=self.directory), \
             mock.patch.object(recovery, "compose_services", return_value=healthy), \
             mock.patch.object(recovery, "candidate_image_identity", return_value=("sha256:image", [])), \
             mock.patch.object(recovery, "compose_result", side_effect=compose), \
             mock.patch.object(recovery, "run", side_effect=run):
            with self.assertRaisesRegex(recovery.RecoveryError, "differs") as err:
                recovery.backup(args)
        self.assertNotIn(self.key1, str(err.exception))
        self.assertFalse(any(call and call[0] == "stop" for call in calls))
        self.assertFalse(args.output.exists())

    def test_fails_closed_for_stopped_containers_and_malformed_inspection(self):
        with self.assertRaisesRegex(recovery.RecoveryError, "both be running"):
            recovery.verify_running_credential_keyrings(self.directory, self.env, "test", [], self.values)
        for api in [None, [], self.entries() + ["DATA_FABRIC_CREDENTIAL_KEYS=secret-value"],
                    ["DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=key-a", "DATA_FABRIC_CREDENTIAL_KEYS={secret-value"]]:
            with self.subTest(api_type=type(api).__name__):
                with self.assertRaises(recovery.RecoveryError) as err:
                    self.verify(api, self.entries())
                self.assertNotIn("secret-value", str(err.exception))
                self.assertNotIn(self.key1, str(err.exception))


if __name__ == "__main__":
    unittest.main()
