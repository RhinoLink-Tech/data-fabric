import hashlib
import json
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from zipfile import ZipFile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
PLACEHOLDER_LABELS = re.compile("\\b(?:candidate|demo|probe)\\b")


class PackageTest(unittest.TestCase):
    def assert_no_placeholder_labels(self, text):
        self.assertNotRegex(text.lower(), PLACEHOLDER_LABELS)

    def test_placeholder_guard_rejects_each_label(self):
        for label in ("candidate", "demo", "probe"):
            with self.subTest(label=label), self.assertRaises(AssertionError):
                self.assert_no_placeholder_labels("Example " + label.upper() + " text")
        self.assert_no_placeholder_labels("probing and demonstration are ordinary words")

    def test_build_is_reproducible(self):
        with tempfile.TemporaryDirectory() as one, tempfile.TemporaryDirectory() as two:
            for target in (one, two):
                subprocess.run([sys.executable, str(HERE / "build_packages.py"), target, "--connector-source", "data-fabric-access"], check=True)
            for name in ("data-fabric-workbuddy-connector.zip", "data-fabric-workbuddy-skill.zip"):
                self.assertEqual(hashlib.sha256((Path(one) / name).read_bytes()).digest(), hashlib.sha256((Path(two) / name).read_bytes()).digest())

    def test_optional_base_url_mode_uses_complete_https_origin(self):
        with tempfile.TemporaryDirectory() as tmp:
            subprocess.run([sys.executable, str(HERE / "build_packages.py"), tmp, "--url-mode", "base-url"], check=True)
            with ZipFile(Path(tmp) / "data-fabric-workbuddy-connector.zip") as archive, \
                 ZipFile(Path(tmp) / "data-fabric-workbuddy-skill.zip") as standalone:
                meta = json.loads(archive.read("connector-meta.json"))
                config = json.loads(archive.read("mcp.json"))
                fields = {f["key"]: f for f in json.loads(archive.read("token-schema.json"))["fields"]}
                server = config["mcpServers"][meta["source"]]
                self.assertEqual(server["url"], "${DATA_FABRIC_BASE_URL}/mcp")
                self.assertEqual(server["url"].replace("${DATA_FABRIC_BASE_URL}", "https://example.test:8443"), "https://example.test:8443/mcp")
                self.assertEqual(fields["DATA_FABRIC_BASE_URL"]["type"], "text")
                self.assertTrue(fields["DATA_FABRIC_BASE_URL"]["required"])
                self.assertNotIn("DATA_FABRIC_MCP_ENDPOINT", fields)
                guide = standalone.read("data-fabric-access/references/workbuddy-setup-zh.md").decode()
                self.assertIn("完整 HTTPS 服务根地址", guide)
                self.assertIn("不含 /mcp 和末尾斜杠", guide)

    def test_enterprise_source_matches_mcp_server(self):
        with tempfile.TemporaryDirectory() as tmp:
            subprocess.run([sys.executable, str(HERE / "build_packages.py"), tmp, "--connector-source", "data-fabric-access"], check=True)
            with ZipFile(Path(tmp) / "data-fabric-workbuddy-connector.zip") as archive:
                meta = json.loads(archive.read("connector-meta.json"))
                config = json.loads(archive.read("mcp.json"))
                self.assertEqual(meta["source"], "data-fabric-access")
                self.assertEqual(list(config["mcpServers"]), ["data-fabric-access"])
                self.assertEqual(config["mcpServers"]["data-fabric-access"]["url"], "https://${DATA_FABRIC_MCP_ENDPOINT}")

    def test_archives_preserve_skill_and_reference_credential_form(self):
        with tempfile.TemporaryDirectory() as tmp:
            subprocess.run([sys.executable, str(HERE / "build_packages.py"), tmp], check=True)
            with ZipFile(Path(tmp) / "data-fabric-workbuddy-connector.zip") as connector, \
                 ZipFile(Path(tmp) / "data-fabric-workbuddy-skill.zip") as standalone:
                self.assertIsNone(connector.testzip())
                self.assertIsNone(standalone.testzip())
                meta = json.loads(connector.read("connector-meta.json"))
                config = json.loads(connector.read("mcp.json"))
                form = json.loads(connector.read("token-schema.json"))
                for key in ("name", "name_en", "description", "description_zh", "description_en", "source", "examples_zh", "examples_en"):
                    self.assertTrue(meta.get(key), key)
                self.assertEqual(meta["auth_mode"], "token")
                self.assertEqual(meta["minWorkbuddyVersion"], "4.24.0")
                self.assertEqual(len(config["mcpServers"]), 1)
                server = config["mcpServers"][meta["source"]]
                self.assertEqual(server["type"], "streamableHttp")
                # WorkBuddy rejects URL templates without an explicit protocol.
                self.assertEqual(server["url"], "https://${DATA_FABRIC_MCP_ENDPOINT}")
                self.assertEqual(server["url"].replace("${DATA_FABRIC_MCP_ENDPOINT}", "example.test:8443/mcp"), "https://example.test:8443/mcp")
                refs = set(re.findall(r"\$\{([A-Z_]+)\}", json.dumps(server)))
                fields = {field["key"]: field for field in form["fields"]}
                self.assertEqual(refs, set(fields))
                self.assertEqual(fields["DATA_FABRIC_ACCESS_TOKEN"]["type"], "password")
                self.assertTrue(all(field["required"] for field in fields.values()))
                for path in (ROOT / "skills/data-fabric-query").rglob("*"):
                    if not path.is_file():
                        continue
                    rel = path.relative_to(ROOT / "skills/data-fabric-query").as_posix()
                    a = connector.read("skills/data-fabric-access/" + rel)
                    b = standalone.read("data-fabric-access/" + rel)
                    self.assertEqual(a, b)
                    if rel.endswith(".md"):
                        self.assert_no_placeholder_labels(a.decode())
                    if rel != "SKILL.md":
                        self.assertEqual(a, path.read_bytes())
                    else:
                        original_body = path.read_text().split("\n---\n", 1)[1]
                        self.assertEqual(a.decode().split("\n---\n", 1)[1], original_body)
                        for key in ("display_name:", "display_name_en:", "description_zh:",
                                    "description_en:", "version:", "author:"):
                            self.assertIn(key, a.decode().split("\n---\n", 1)[0])
                guide_path = "references/workbuddy-setup-zh.md"
                entry = standalone.read("data-fabric-access/SKILL.md").decode()
                self.assertIn(guide_path, entry)
                guide = standalone.read("data-fabric-access/" + guide_path).decode()
                self.assertIn("list_source_types", guide)
                self.assertIn("begin_data_source_update", guide)
                self.assertIn("begin_data_source_onboarding", entry)
                self.assertIn("get_data_source_onboarding_status", entry)
                self.assertIn("begin_data_source_update", entry)
                self.assertIn("get_data_source_update_status", entry)
                self.assert_no_placeholder_labels(entry)
                self.assertIn("allowed_by_instance_type_policy", entry)
                self.assertIn("allowed_by_onboarding_type_policy", entry)
                self.assertIn("TLS with verified server identity", entry)
                self.assertIn("These tools must be enabled", entry)
                self.assertIn("自己部署", meta["description_zh"])
                self.assertEqual(meta["version"], "1.2.0")
                self.assertEqual(meta["source"], "data-fabric-selfhost")
                self.assertIn("name: data-fabric-access", entry.split("\n---\n", 1)[0])
                self.assertIn("manage an existing source", entry.lower())
                self.assertTrue(connector.read("icon.png").startswith(b"\x89PNG\r\n\x1a\n"))


if __name__ == "__main__":
    unittest.main()
