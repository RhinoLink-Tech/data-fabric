"""Exercise portable source archive checksums from a separate download directory."""

import hashlib
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("source_archive_checksum.sh")


class SourceArchiveChecksumTests(unittest.TestCase):
    def test_downloaded_archive_is_checked_without_build_host_path(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / "build host"
            download = root / "clean download"
            source.mkdir()
            download.mkdir()
            archive = source / "data-fabric-v2.1.0-preview.2-source.tar.gz"
            archive.write_bytes(b"synthetic release source archive\n")

            subprocess.run(["bash", str(SCRIPT), str(archive)], check=True, cwd=root,
                           capture_output=True, text=True)
            checksum = archive.with_name(archive.name + ".sha256")
            expected = hashlib.sha256(archive.read_bytes()).hexdigest()
            self.assertEqual(checksum.read_text(), f"{expected}  {archive.name}\n")

            downloaded_archive = download / archive.name
            downloaded_checksum = download / checksum.name
            downloaded_archive.write_bytes(archive.read_bytes())
            downloaded_checksum.write_bytes(checksum.read_bytes())

            def verify():
                return subprocess.run(["sha256sum", "-c", checksum.name], cwd=download,
                                      capture_output=True, text=True).returncode

            self.assertEqual(verify(), 0)
            downloaded_archive.unlink()
            self.assertTrue(archive.exists())
            self.assertNotEqual(verify(), 0, "must not fall back to the build-host archive")
            downloaded_archive.write_bytes(b"tampered download")
            self.assertNotEqual(verify(), 0, "modified downloads must fail verification")

    def test_missing_source_does_not_create_checksum(self):
        with tempfile.TemporaryDirectory() as temp:
            archive = Path(temp) / "missing.tar.gz"
            result = subprocess.run(["bash", str(SCRIPT), str(archive)],
                                    capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(archive.with_name(archive.name + ".sha256").exists())


if __name__ == "__main__":
    unittest.main()
