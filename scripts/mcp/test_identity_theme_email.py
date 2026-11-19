"""Contract checks for the Keycloak 26.3.5 identity email theme."""
import re
import unittest
from pathlib import Path


class IdentityThemeEmailTests(unittest.TestCase):
    def test_email_messages_keep_keycloak_arguments(self):
        theme = Path(__file__).resolve().parents[2] / "deploy/mcp/identity-theme/data-fabric/email/messages/messages_en.properties"
        messages = {}
        for line in theme.read_text(encoding="utf-8").splitlines():
            if line and not line.startswith("#"):
                key, _, value = line.partition("=")
                messages[key] = value

        # Keycloak 26.3.5 base email templates call these names with these
        # argument positions. In particular {0} is the action link or code.
        contracts = {
            "emailVerificationBody": {"0", "2", "3"},
            "emailVerificationBodyHtml": {"0", "2", "3"},
            "emailVerificationBodyCode": {"0"},
            "emailVerificationBodyCodeHtml": {"0"},
            "emailUpdateConfirmationBody": {"0", "1", "2", "3"},
            "emailUpdateConfirmationBodyHtml": {"0", "1", "2", "3"},
            "passwordResetBody": {"0", "2", "3"},
            "passwordResetBodyHtml": {"0", "2", "3"},
            "executeActionsBody": {"0", "2", "3", "4"},
            "executeActionsBodyHtml": {"0", "2", "3", "4"},
        }
        for name, expected in contracts.items():
            with self.subTest(message=name):
                self.assertIn(name, messages, "message override missing")
                self.assertEqual(set(re.findall(r"\{(\d+)\}", messages[name])), expected)


if __name__ == "__main__":
    unittest.main()
