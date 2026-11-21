"""Tests for the unauthenticated public MCP OAuth diagnostic."""
import contextlib
import io
import json
import http.server
import shutil
import ssl
import subprocess
import tempfile
import threading
import unittest
import urllib.request
import urllib.error
from unittest import mock

import oauth_probe


RESOURCE = "https://fabric.example/mcp"
ISSUER = "https://identity.example/realm"
ROOT = "https://fabric.example/.well-known/oauth-protected-resource"
PATH = ROOT + "/mcp"


class Response(io.BytesIO):
    def __init__(self, body, status=200, headers=None):
        super().__init__(body)
        self.status = status
        self.headers = headers or {}


class Opener:
    def __init__(self):
        self.calls = []
        self.document = json.dumps({"resource": RESOURCE, "authorization_servers": [ISSUER]}).encode()
        self.overrides = {}

    def open(self, req, timeout):
        self.calls.append((req, timeout))
        value = self.overrides.get(req.full_url)
        if value is not None:
            if isinstance(value, Exception):
                raise value
            return value
        if req.full_url == RESOURCE:
            headers = {"WWW-Authenticate": 'Bearer resource_metadata="' + PATH + '"'}
            raise urllib.error.HTTPError(req.full_url, 401, "Unauthorized", headers, io.BytesIO())
        return Response(self.document)


class OAuthProbeTests(unittest.TestCase):
    def test_success_is_read_only_and_sends_no_credentials(self):
        opener = Opener()
        result = oauth_probe.probe(RESOURCE, ISSUER, opener)
        self.assertTrue(all(ok for ok, _, _ in result))
        self.assertEqual([r.full_url for r, _ in opener.calls], [PATH, ROOT, RESOURCE])
        for req, timeout in opener.calls:
            self.assertEqual(req.get_method(), "GET")
            self.assertIsNone(req.get_header("Authorization"))
            self.assertEqual(timeout, oauth_probe.TIMEOUT_SECONDS)

    def test_invalid_inputs_are_rejected_before_any_request(self):
        for bad in ("http://fabric.example/mcp", "https://user:secret@fabric.example/mcp",
                    "https://fabric.example/mcp?token=secret", "https://fabric.example/other",
                    "https://fabric.example:bad/mcp", " https://fabric.example/mcp"):
            with self.subTest(bad=bad):
                opener = Opener()
                with self.assertRaises(ValueError):
                    oauth_probe.probe(bad, ISSUER, opener)
                self.assertEqual(opener.calls, [])
        with self.assertRaises(ValueError):
            oauth_probe.probe(RESOURCE, "https://identity.example/realm#fragment", Opener())

    def test_metadata_and_challenge_failures_are_independent(self):
        opener = Opener()
        opener.overrides[ROOT] = urllib.error.HTTPError(ROOT, 404, "missing", {}, io.BytesIO(b"secret"))
        opener.overrides[RESOURCE] = urllib.error.HTTPError(RESOURCE, 401, "no metadata", {"WWW-Authenticate": "Bearer"}, io.BytesIO())
        results = oauth_probe.probe(RESOURCE, ISSUER, opener)
        self.assertEqual([ok for ok, _, _ in results], [True, False, False])
        self.assertIn("HTTP 404", results[1][2])
        self.assertIn("does not advertise", results[2][2])
        self.assertNotIn("secret", repr(results))

    def test_metadata_validation_and_response_limit(self):
        for body, expected in [
            (b"not json", "not valid JSON"),
            (json.dumps({"resource": "https://wrong.example/mcp", "authorization_servers": [ISSUER]}).encode(), "resource does not match"),
            (json.dumps({"resource": RESOURCE, "authorization_servers": ["https://wrong.example"]}).encode(), "authorization server does not match"),
            (b"x" * (oauth_probe.MAX_METADATA_BYTES + 1), "too large"),
        ]:
            with self.subTest(expected=expected):
                opener = Opener()
                opener.overrides[PATH] = Response(body)
                result = oauth_probe.probe(RESOURCE, ISSUER, opener)
                self.assertFalse(result[0][0])
                self.assertIn(expected, result[0][2])

    def test_rejects_redirect_and_does_not_claim_authentication(self):
        self.assertIsNone(oauth_probe.NoRedirect().redirect_request(None, None, 302, "", {}, "https://elsewhere.example"))
        opener = Opener()
        opener.overrides[PATH] = urllib.error.HTTPError(PATH, 302, "redirect", {"Location": "https://elsewhere.example"}, io.BytesIO())
        results = oauth_probe.probe(RESOURCE, ISSUER, opener)
        self.assertFalse(results[0][0])
        self.assertIn("HTTP 302", results[0][2])
        output = io.StringIO()
        with mock.patch.object(oauth_probe, "probe", return_value=results), contextlib.redirect_stdout(output):
            self.assertEqual(oauth_probe.main(["--resource", RESOURCE, "--issuer", ISSUER]), 1)
        self.assertIn("login, issued tokens, refresh, permissions and client use were not tested", output.getvalue())

    @unittest.skipUnless(shutil.which("openssl"), "openssl is needed to create a local test certificate")
    def test_real_https_fixture_with_verified_certificate(self):
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                if self.path == "/mcp":
                    self.send_response(401)
                    metadata = ("https://127.0.0.1:" + str(self.server.server_port)
                                + "/.well-known/oauth-protected-resource/mcp")
                    if not self.server.valid_challenge:
                        metadata += "_invalid"
                    self.send_header("WWW-Authenticate", 'Bearer resource_metadata="' + metadata + '"')
                    self.end_headers()
                    return
                doc = json.dumps({"resource": resource, "authorization_servers": [issuer]}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(doc)))
                self.end_headers()
                self.wfile.write(doc)

        with tempfile.TemporaryDirectory() as tmp:
            cert, key = tmp + "/tls.crt", tmp + "/tls.key"
            subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048",
                            "-nodes", "-days", "1", "-subj", "/CN=127.0.0.1",
                            "-addext", "subjectAltName=IP:127.0.0.1",
                            "-keyout", key, "-out", cert], check=True, capture_output=True)
            server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
            resource = "https://127.0.0.1:" + str(server.server_port) + "/mcp"
            server.valid_challenge = True
            issuer = "https://identity.example/realm"
            tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            tls.load_cert_chain(cert, key)
            server.socket = tls.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                trusted = ssl.create_default_context(cafile=cert)
                opener = urllib.request.build_opener(oauth_probe.NoRedirect(),
                                                     urllib.request.HTTPSHandler(context=trusted))
                results = oauth_probe.probe(resource, issuer, opener)
                self.assertEqual([ok for ok, _, _ in results], [True, True, True])
                server.valid_challenge = False
                results = oauth_probe.probe(resource, issuer, opener)
                self.assertEqual([ok for ok, _, _ in results], [True, True, False])
                self.assertIn("does not advertise", results[2][2])
                untrusted = urllib.request.build_opener(oauth_probe.NoRedirect(),
                                                        urllib.request.HTTPSHandler(context=ssl.create_default_context()))
                self.assertFalse(any(ok for ok, _, _ in oauth_probe.probe(resource, issuer, untrusted)))
            finally:
                server.shutdown()
                thread.join()
                server.server_close()

    def test_network_error_and_bad_challenge(self):
        opener = Opener()
        opener.overrides[PATH] = urllib.error.URLError("synthetic-secret")
        opener.overrides[RESOURCE] = urllib.error.HTTPError(RESOURCE, 401, "", {"WWW-Authenticate": 'Bearer resource_metadata="https://wrong.example"'}, io.BytesIO())
        results = oauth_probe.probe(RESOURCE, ISSUER, opener)
        self.assertEqual([ok for ok, _, _ in results], [False, True, False])
        self.assertNotIn("synthetic-secret", repr(results))


if __name__ == "__main__":
    unittest.main()
