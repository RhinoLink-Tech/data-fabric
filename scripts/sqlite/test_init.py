import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("sqlite_init", Path(__file__).with_name("init.py"))
sqlite_init = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(sqlite_init)


class SQLiteInitializationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        source = sqlite_init.DEFAULT_DIRECTORY / ".env.example"
        (self.directory / ".env.example").write_text(source.read_text(encoding="utf-8"), encoding="utf-8")

    def test_initialization_is_private_and_idempotent(self):
        with patch.object(sqlite_init, "metadata_volume_exists", return_value=False):
            sqlite_init.initialize(self.directory, "isolated")
            env = self.directory / ".env"
            first = env.read_text(encoding="utf-8")
            self.assertEqual(env.stat().st_mode & 0o777, 0o600)
            self.assertEqual((self.directory / "tls" / "tls.key").stat().st_mode & 0o777, 0o640)
            sqlite_init.initialize(self.directory, "isolated")
            self.assertEqual(first, env.read_text(encoding="utf-8"))

    def test_missing_keys_with_existing_volume_are_not_replaced(self):
        with patch.object(sqlite_init, "metadata_volume_exists", return_value=True):
            with self.assertRaises(sqlite_init.onboard.OnboardingError):
                sqlite_init.initialize(self.directory, "isolated")
        self.assertFalse((self.directory / ".env").exists())

    def test_existing_incomplete_configuration_is_not_replaced(self):
        env = self.directory / ".env"
        env.write_text("JWT_SECRET=\n", encoding="utf-8")
        os.chmod(env, 0o600)
        with self.assertRaises(sqlite_init.onboard.OnboardingError):
            sqlite_init.initialize(self.directory, "isolated")
        self.assertEqual(env.read_text(encoding="utf-8"), "JWT_SECRET=\n")

    def test_symlink_is_rejected(self):
        env = self.directory / ".env"
        target = self.directory / "existing"
        target.write_text("keep", encoding="utf-8")
        env.symlink_to(target)
        with self.assertRaises(sqlite_init.onboard.OnboardingError):
            sqlite_init.initialize(self.directory, "isolated")
        self.assertEqual(target.read_text(encoding="utf-8"), "keep")


if __name__ == "__main__":
    unittest.main()
