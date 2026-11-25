"""Regression coverage for release-archive provenance checks."""
import base64
import hashlib
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

import recovery


class PublishedReleaseCatalogTests(unittest.TestCase):
    def test_preview3_identity_is_explicitly_allowlisted(self):
        release = recovery.RELEASE_SOURCE_ARCHIVES[
            "ebb4a0ef456447f90ae3b1f1d3fe3d2719785974fc82a746a2e2278dae8da0c1"
        ]
        self.assertEqual(release["root"], "data-fabric-v2.1.0-preview.3")
        self.assertEqual(release["revision"], "2b2a218b209edb7d67ef02b4762d195f0622cd3c")
        self.assertEqual(
            release["image"],
            "ghcr.io/rhinolink-tech/data-fabric:v2.1.0-preview.3@sha256:"
            "0c5ada5c16f9a411ecc80f8fd1635733ab7e88cb5f2bdc2d74f27b54ca6278af",
        )


class ReleaseArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.root = self.base / "extracted"
        (self.root / "deploy" / "mcp").mkdir(parents=True)
        self.config = self.root / "deploy" / "mcp" / "docker-compose.yml"
        self.config.write_text("services: {}\n")
        self.archive = self.base / "release.tar.gz"
        with tarfile.open(self.archive, "w:gz") as tf:
            content = self.config.read_bytes()
            info = tarfile.TarInfo("fixture/deploy/mcp/docker-compose.yml")
            info.mode = 0o644
            info.size = len(content)
            tf.addfile(info, io.BytesIO(content))
        digest = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        self.release = {"root": "fixture", "revision": "a" * 40, "image": "example/image@sha256:" + "b" * 64}
        patcher = mock.patch.dict(recovery.RELEASE_SOURCE_ARCHIVES, {digest: self.release}, clear=True)
        patcher.start()
        self.addCleanup(patcher.stop)

    def identity(self):
        return recovery.release_source_identity(self.root, self.archive)

    def test_accepts_matching_archive_and_tree(self):
        self.assertEqual(self.identity(), (self.release["revision"], self.release["image"]))

    def test_rejects_changed_missing_or_executable_source(self):
        for change in ("content", "missing", "mode"):
            with self.subTest(change=change):
                self.config.write_text("services: {}\n")
                self.config.chmod(0o644)
                if change == "content": self.config.write_text("services: changed\n")
                elif change == "missing": self.config.unlink()
                else: self.config.chmod(0o755)
                with self.assertRaises(recovery.RecoveryError): self.identity()

    def test_rejects_symlink_and_unknown_archive(self):
        self.config.unlink()
        other = self.base / "other"
        other.write_text("services: {}\n")
        self.config.symlink_to(other)
        with self.assertRaises(recovery.RecoveryError): self.identity()
        self.config.unlink()
        self.config.write_text("services: {}\n")
        self.archive.write_bytes(self.archive.read_bytes() + b"changed")
        with self.assertRaisesRegex(recovery.RecoveryError, "checksum"): self.identity()

    def test_wrong_image_fails_before_compose(self):
        key = base64.b64encode(bytes(range(32))).decode("ascii")
        env = self.config.parent / ".env"
        env.write_text("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=k\n" +
                       "DATA_FABRIC_CREDENTIAL_KEYS=" + json.dumps({"k": key}) + "\n" +
                       "DATA_FABRIC_SOURCE_REVISION=unknown\nDATA_FABRIC_IMAGE=wrong\n")
        env.chmod(0o600)
        args = SimpleNamespace(directory=self.config.parent, project_name="test-project",
                               release_source_archive=self.archive, output=self.base / "backup.age",
                               recipient="test", staging_dir=self.base / "staging")
        with mock.patch.object(recovery, "compose_services") as compose:
            with self.assertRaisesRegex(recovery.RecoveryError, "configured image"):
                recovery.backup(args)
            compose.assert_not_called()
        self.assertFalse(args.output.exists())

    def test_conflicting_revision_fails_before_compose(self):
        key = base64.b64encode(bytes(range(32))).decode("ascii")
        env = self.config.parent / ".env"
        env.write_text("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=k\n" +
                       "DATA_FABRIC_CREDENTIAL_KEYS=" + json.dumps({"k": key}) + "\n" +
                       "DATA_FABRIC_SOURCE_REVISION=" + "c" * 40 + "\n" +
                       "DATA_FABRIC_IMAGE=" + self.release["image"] + "\n")
        env.chmod(0o600)
        args = SimpleNamespace(directory=self.config.parent, project_name="test-project",
                               release_source_archive=self.archive, output=self.base / "backup.age",
                               recipient="test", staging_dir=self.base / "staging")
        with mock.patch.object(recovery, "compose_services") as compose:
            with self.assertRaisesRegex(recovery.RecoveryError, "source revision conflicts"):
                recovery.backup(args)
            compose.assert_not_called()
        self.assertFalse(args.output.exists())

    def test_restore_mismatch_fails_before_creating_payload(self):
        target = self.config.parent
        (target / "docker-compose.recovery.yml").write_text("services: {}\n")
        (target / ".env.example").write_text("")
        work = self.base / "decrypted"
        payload = work / "payload"
        payload.mkdir(parents=True)
        args = SimpleNamespace(archive=self.base / "backup.age", identity_file=self.base / "identity",
                               staging_dir=self.base / "staging", target_deploy_dir=target,
                               project_name="restore-project", api_port=18080, mcp_port=18443,
                               release_source_archive=self.archive)
        manifest = {"source_revision": "a" * 40, "app_image_reference": self.release["image"]}
        with mock.patch.object(recovery, "require_fresh_compose_project"), \
             mock.patch.object(recovery, "require_free_loopback_port"), \
             mock.patch.object(recovery, "decrypt_archive", return_value=(work, payload)), \
             mock.patch.object(recovery, "load_and_validate_payload", return_value=manifest), \
             mock.patch.object(recovery, "release_source_identity", return_value=("c" * 40, self.release["image"])), \
             mock.patch.object(recovery, "run") as run:
            with self.assertRaisesRegex(recovery.RecoveryError, "does not match the backup"):
                recovery.restore(args)
            run.assert_not_called()
        self.assertFalse((target / ".env").exists())
        self.assertFalse((target / "recovery").exists())

    def test_concurrent_env_creation_is_preserved(self):
        target = self.config.parent
        (target / "docker-compose.recovery.yml").write_text("services: {}\n")
        (target / ".env.example").write_text("")
        env_path = target / ".env"
        competing = b"OPERATOR_SECRET_PLACEHOLDER=keep\n"
        work = self.base / "decrypted"
        payload = work / "payload"
        payload.mkdir(parents=True)
        image = self.release["image"]
        (payload / "deployment.env").write_text(
            f"DATA_FABRIC_IMAGE={image}\nDATA_FABRIC_SOURCE_REVISION={'a' * 40}\n")
        (payload / "metadata.sql").write_bytes(b"SELECT 1;\n")
        args = SimpleNamespace(archive=self.base / "backup.age", identity_file=self.base / "identity",
                               staging_dir=self.base / "staging", target_deploy_dir=target,
                               project_name="restore-project", api_port=18080, mcp_port=18443,
                               release_source_archive=None)
        manifest = {"source_revision": "a" * 40, "app_image_reference": image,
                    "app_image_id": "sha256:" + "c" * 64}
        real_open = recovery.os.open
        raced = []

        def racing_open(path, flags, *args, **kwargs):
            if recovery.os.fspath(path) == str(env_path) and not raced:
                env_path.write_bytes(competing)
                env_path.chmod(0o640)
                raced.append(True)
            return real_open(path, flags, *args, **kwargs)

        image_inspect = SimpleNamespace(returncode=0, stdout=f"sha256:{'c' * 64}|{'a' * 40}\n")
        with mock.patch.object(recovery, "require_fresh_compose_project"), \
             mock.patch.object(recovery, "require_free_loopback_port"), \
             mock.patch.object(recovery, "decrypt_archive", return_value=(work, payload)), \
             mock.patch.object(recovery, "load_and_validate_payload", return_value=manifest), \
             mock.patch.object(recovery, "git_revision", return_value="a" * 40), \
             mock.patch.object(recovery, "run", return_value=image_inspect), \
             mock.patch.object(recovery.os, "open", side_effect=racing_open):
            with self.assertRaises(recovery.RecoveryError):
                recovery.restore(args)
        self.assertTrue(raced)
        self.assertEqual(env_path.read_bytes(), competing)
        self.assertEqual(env_path.stat().st_mode & 0o777, 0o640)
        self.assertFalse((target / "recovery").exists())

    def test_failure_after_own_env_creation_cleans_own_outputs(self):
        target = self.config.parent
        (target / "docker-compose.recovery.yml").write_text("services: {}\n")
        (target / ".env.example").write_text("")
        env_path = target / ".env"
        work = self.base / "decrypted"
        payload = work / "payload"
        payload.mkdir(parents=True)
        image = self.release["image"]
        (payload / "deployment.env").write_text(
            f"DATA_FABRIC_IMAGE={image}\nDATA_FABRIC_SOURCE_REVISION={'a' * 40}\n")
        (payload / "metadata.sql").write_bytes(b"SELECT 1;\n")
        args = SimpleNamespace(archive=self.base / "backup.age", identity_file=self.base / "identity",
                               staging_dir=self.base / "staging", target_deploy_dir=target,
                               project_name="restore-project", api_port=18080, mcp_port=18443,
                               release_source_archive=None)
        manifest = {"source_revision": "a" * 40, "app_image_reference": image,
                    "app_image_id": "sha256:" + "c" * 64}
        image_inspect = SimpleNamespace(returncode=0, stdout=f"sha256:{'c' * 64}|{'a' * 40}\n")
        with mock.patch.object(recovery, "require_fresh_compose_project"), \
             mock.patch.object(recovery, "require_free_loopback_port"), \
             mock.patch.object(recovery, "decrypt_archive", return_value=(work, payload)), \
             mock.patch.object(recovery, "load_and_validate_payload", return_value=manifest), \
             mock.patch.object(recovery, "git_revision", return_value="a" * 40), \
             mock.patch.object(recovery, "run", return_value=image_inspect), \
             mock.patch.object(recovery.shutil, "copyfileobj", side_effect=OSError("disk full")):
            with self.assertRaisesRegex(OSError, "disk full"):
                recovery.restore(args)
        self.assertFalse(env_path.exists())
        self.assertFalse((target / "recovery").exists())


if __name__ == "__main__":
    unittest.main()
