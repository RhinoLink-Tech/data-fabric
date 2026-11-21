import json
import subprocess
import unittest
from unittest.mock import patch

import volume_snapshot as snapshot


class SourceSelectionTest(unittest.TestCase):
    def config(self, path):
        service = {"environment": {"DATABASE_SQLITE_PATH": path}}
        return json.dumps({"services": {"api": service, "mcp": service}}).encode()

    def test_stopped_profile_uses_configured_restored_file(self):
        with patch.object(snapshot, "compose", side_effect=[self.config("/var/lib/data-fabric/restored.sqlite"), b"", b""]):
            self.assertEqual(snapshot.current_database("sample"), "restored.sqlite")

    def test_running_service_with_stale_path_is_rejected(self):
        container = [{"Config": {"Env": ["DATABASE_SQLITE_PATH=/var/lib/data-fabric/old.sqlite"]},
                      "Mounts": [{"Destination": snapshot.VOLUME, "Type": "volume", "Name": "sample_metadata"}]}]
        completed = subprocess.CompletedProcess([], 0, stdout=json.dumps(container).encode())
        with patch.object(snapshot, "compose", side_effect=[self.config("/var/lib/data-fabric/restored.sqlite"), b"container"]), \
             patch.object(snapshot.subprocess, "run", return_value=completed):
            with self.assertRaisesRegex(snapshot.SnapshotError, "differs"):
                snapshot.current_database("sample")

    def test_path_outside_volume_is_rejected(self):
        with patch.object(snapshot, "compose", return_value=self.config("/tmp/metadata.sqlite")):
            with self.assertRaises(snapshot.SnapshotError):
                snapshot.current_database("sample")


if __name__ == "__main__":
    unittest.main()
