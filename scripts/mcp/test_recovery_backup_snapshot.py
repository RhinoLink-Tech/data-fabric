"""Backup preflight side effects with synthetic Docker, database and age responses."""
import base64
import contextlib
import io
import json
import os
import tarfile
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

import recovery


class BackupSnapshotTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.directory = self.root / "profile"
        self.directory.mkdir()
        (self.directory / "docker-compose.yml").write_text("services: {}\n")
        self.env = self.directory / ".env"
        key = base64.b64encode(b"a" * 32).decode("ascii")
        self.snapshot = ("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=key-a\n"
                         + "DATA_FABRIC_CREDENTIAL_KEYS=" + json.dumps({"key-a": key})
                         + "\nDATA_FABRIC_SOURCE_REVISION=revision\nDATA_FABRIC_IMAGE=local-image\n").encode()
        self.env.write_bytes(self.snapshot)
        self.env.chmod(0o600)
        self.changed = self.snapshot + b"# changed after preflight\n"
        self.args = SimpleNamespace(directory=self.directory, project_name="test", output=self.root / "result.age",
                                    recipient="synthetic-recipient", staging_dir=self.root / "staging", release_source_archive=None)
        self.ids = {recovery.APP_SERVICES[0]: "api-id", recovery.APP_SERVICES[1]: "mcp-id"}
        self.running = set(recovery.APP_SERVICES)
        self.calls = []
        self.archived_env = None
        self.manifest = None
        self.stop_action = None
        self.dump_action = None
        self.audit_supported = True
        self.audit_failure = False
        self.audit_invalid_output = False
        self.healthy = {name: {"State": "running", "Health": "healthy"}
                        for name in (*recovery.APP_SERVICES, "metadata-db")}

    def compose(self, directory, env, project, *args):
        self.calls.append(args)
        if args == ("ps", "--services", "--status", "running"):
            return SimpleNamespace(stdout="\n".join(self.running))
        if args == ("ps", "-q", "metadata-db"):
            return SimpleNamespace(stdout="db-id")
        if args[:3] == ("ps", "--all", "-q"):
            return SimpleNamespace(stdout=self.ids[args[3]])
        if args[0] == "stop":
            self.running.difference_update(args[3:])
            if self.stop_action:
                self.stop_action()
            return SimpleNamespace(stdout="")
        raise AssertionError(f"Unexpected compose operation: {args}")

    def fake_run(self, command, **kwargs):
        if command[:2] == ["docker", "exec"] and command[-1] == "/app/credential-audit":
            self.calls.append(("audit", command[2]))
            return SimpleNamespace(returncode=1 if self.audit_failure else 0,
                                   stdout="" if self.audit_failure or self.audit_invalid_output else "Verified credentials for 2 stored data sources\n",
                                   stderr="synthetic-secret should not appear")
        if command[:2] == ["docker", "inspect"]:
            return SimpleNamespace(returncode=0, stdout="sha256:db|mysql:fixture", stderr="")
        if "start" in command:
            self.calls.append(("start", *command[command.index("start") + 1:]))
            self.running.update(command[command.index("start") + 1:])
            return SimpleNamespace(returncode=0, stdout="", stderr="")
        if "exec" in command:
            return SimpleNamespace(returncode=0, stdout="8.4.13\n", stderr="")
        raise AssertionError(f"Unexpected run operation: {command}")

    def subprocess_run(self, command, **kwargs):
        if command[0] == "age":
            with tarfile.open(command[-1], "r:gz") as archive:
                self.archived_env = archive.extractfile("deployment.env").read()
                self.manifest = json.load(archive.extractfile("manifest.json"))
            os.write(kwargs["stdout"], b"synthetic-encrypted-bytes")
            return SimpleNamespace(returncode=0)
        if "mysqldump" in command[-1]:
            if self.dump_action:
                self.dump_action()
            kwargs["stdout"].write(b"-- synthetic metadata\n")
            return SimpleNamespace(returncode=0)
        raise AssertionError("Unexpected subprocess")

    def execute(self):
        with contextlib.ExitStack() as stack:
            for obj, name, kwargs in [
                (recovery, "git_revision", {"return_value": "revision"}),
                (recovery.shutil, "which", {"return_value": "/synthetic/age"}),
                (recovery, "secure_staging_root", {"return_value": self.root}),
                (recovery, "compose_services", {"return_value": self.healthy}),
                (recovery, "candidate_image_identity", {"return_value": ("sha256:image", [])}),
                (recovery, "verify_running_credential_keyrings", {"return_value": dict(self.ids)}),
                (recovery, "source_audit_supported", {"return_value": self.audit_supported}),
                (recovery, "compose_result", {"side_effect": self.compose}),
                (recovery, "run", {"side_effect": self.fake_run}),
                (recovery.subprocess, "run", {"side_effect": self.subprocess_run}),
            ]:
                stack.enter_context(mock.patch.object(obj, name, **kwargs))
            stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
            return recovery.backup(self.args)

    def assert_restart_attempt_and_no_archive(self):
        # Fake service-name bookkeeping does not prove which actual containers restarted.
        self.assertTrue(any(call[0] == "start" and set(call[1:]) == set(recovery.APP_SERVICES)
                            for call in self.calls))
        self.assertFalse(self.args.output.exists())
        self.assertIsNone(self.archived_env)

    def test_failed_credential_audit_aborts_before_stopping(self):
        self.audit_failure = True
        with self.assertRaisesRegex(recovery.RecoveryError, "source credential audit failed") as caught:
            self.execute()
        self.assertNotIn("synthetic-secret", str(caught.exception))
        self.assertIn(("audit", "api-id"), self.calls)
        self.assertFalse(any(call[0] in ("stop", "start") for call in self.calls))
        self.assertFalse(self.args.output.exists())

    def test_audit_without_success_marker_aborts_before_stopping(self):
        self.audit_invalid_output = True
        with self.assertRaisesRegex(recovery.RecoveryError, "source credential audit failed"):
            self.execute()
        self.assertFalse(any(call[0] in ("stop", "start") for call in self.calls))
        self.assertFalse(self.args.output.exists())

    def test_successful_audit_runs_before_stopping(self):
        self.assertEqual(self.execute(), 0)
        audit = self.calls.index(("audit", "api-id"))
        stop = next(i for i, call in enumerate(self.calls) if call[0] == "stop")
        self.assertLess(audit, stop)

    def test_legacy_profile_keeps_existing_backup_path(self):
        self.audit_supported = False
        self.assertEqual(self.execute(), 0)
        self.assertFalse(any(call[0] == "audit" for call in self.calls))

    def test_container_replaced_after_stop_aborts_and_attempts_restart(self):
        def replace():
            self.ids[recovery.APP_SERVICES[0]] = "replacement-id"
        self.stop_action = replace
        with self.assertRaisesRegex(recovery.RecoveryError, "container changed"):
            self.execute()
        self.assert_restart_attempt_and_no_archive()

    def test_env_change_after_stop_aborts_and_restarts(self):
        self.stop_action = lambda: self.env.write_bytes(self.changed)
        with self.assertRaisesRegex(recovery.RecoveryError, "deployment .env changed"):
            self.execute()
        self.assert_restart_attempt_and_no_archive()

    def test_success_archives_verified_snapshot_even_if_env_changes_during_dump(self):
        # The preflight checks have finished before the dump; copy must still use
        # the snapshot validated against both containers rather than reread .env.
        self.dump_action = lambda: self.env.write_bytes(self.changed)
        self.assertEqual(self.execute(), 0)
        self.assertEqual(self.archived_env, self.snapshot)
        self.assertEqual(self.env.read_bytes(), self.changed)
        self.assertEqual(self.manifest["files"]["deployment.env"]["sha256"],
                         __import__("hashlib").sha256(self.snapshot).hexdigest())
        self.assertTrue(self.args.output.exists())
        # This mock checks the start attempt, not real Docker or service health.
        self.assertTrue(any(call[0] == "start" for call in self.calls))


if __name__ == "__main__":
    unittest.main()
