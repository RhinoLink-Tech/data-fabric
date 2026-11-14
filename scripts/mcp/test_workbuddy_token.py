import base64
import hashlib
import http.client
import http.server
import importlib.util
from pathlib import Path
import threading
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("helper", Path(__file__).with_name("workbuddy_token.py"))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


class PkceTests(unittest.TestCase):
    def test_challenge_is_s256_and_unique(self):
        verifier, challenge = helper.pkce()
        expected = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
        self.assertEqual(expected, challenge)
        self.assertNotEqual(verifier, helper.pkce()[0])

    def test_discovery_pins_issuer_and_https_endpoints(self):
        metadata = {"issuer": "https://identity.test/realm", "authorization_endpoint": "https://identity.test/auth", "token_endpoint": "https://identity.test/token"}
        with patch.object(helper, "request_json", return_value=metadata):
            self.assertEqual(helper.discovery("https://identity.test/realm"), ("https://identity.test/auth", "https://identity.test/token"))
            with self.assertRaises(ValueError):
                helper.discovery("https://identity.test/other")
            metadata["token_endpoint"] = 7
            with self.assertRaises(ValueError):
                helper.discovery("https://identity.test/realm")
            metadata["token_endpoint"] = "http://identity.test/token"
            with self.assertRaises(ValueError):
                helper.discovery("https://identity.test/realm")

    def test_callback_rejects_wrong_state_host_and_duplicates(self):
        result = {}
        server = http.server.HTTPServer(("127.0.0.1", 0), http.server.BaseHTTPRequestHandler)
        port = server.server_port
        server.RequestHandlerClass = helper.callback_handler("valid-state", port, result)
        server.timeout = .1
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        def req(path, host):
            conn = http.client.HTTPConnection("127.0.0.1", port, timeout=2)
            conn.request("GET", path, headers={"Host": host})
            response = conn.getresponse()
            body = response.read()
            conn.close()
            return response.status, body
        try:
            for path, host in [('/callback?state=wrong&code=x', f'127.0.0.1:{port}'), ('/callback?state=valid-state&code=x', 'evil.test'), ('/callback?state=valid-state&code=x&code=y', f'127.0.0.1:{port}')]:
                self.assertEqual(req(path, host)[0], 400)
                self.assertEqual(result, {})
            status, body = req('/callback?state=valid-state&code=synthetic-code', f'127.0.0.1:{port}')
            self.assertEqual(status, 200)
            self.assertNotIn(b'synthetic-code', body)
            self.assertEqual(result, {'code': 'synthetic-code'})
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_reject_http_urls_and_userinfo(self):
        for url in ('http://example.test', 'https://user:pass@example.test', 'https://example.test/#fragment'):
            with self.assertRaises(ValueError):
                helper.https_url(url)


    def test_complete_mock_flow_and_reject_missing_scope(self):
        import argparse
        import socket
        import urllib.parse
        s = socket.socket()
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
        s.close()
        args = argparse.Namespace(issuer="https://identity.test/realm", client_id="public-client", port=port,
                                  scope="openid data-fabric.query data-fabric.sources.manage", resource="https://mcp.test/mcp")
        captured = {}
        granted = {"scope": "openid data-fabric.query", "token_type": "Bearer"}
        def browser(url):
            query = urllib.parse.parse_qs(urllib.parse.urlsplit(url).query)
            captured.update(query)
            def callback():
                conn = http.client.HTTPConnection("127.0.0.1", port, timeout=2)
                conn.request("GET", "/callback?" + urllib.parse.urlencode({"state": query["state"][0], "code": "synthetic-code"}))
                conn.getresponse().read()
                conn.close()
            threading.Thread(target=callback, daemon=True).start()
            return True
        def token_request(url, data):
            self.assertEqual(url, "https://identity.test/token")
            self.assertEqual(data["code"], "synthetic-code")
            self.assertEqual(data["resource"], args.resource)
            digest = base64.urlsafe_b64encode(hashlib.sha256(data["code_verifier"].encode()).digest()).rstrip(b"=").decode()
            self.assertEqual(digest, captured["code_challenge"][0])
            return {"access_token": "synthetic-token", "token_type": granted["token_type"], "expires_in": 300, "scope": granted["scope"]}
        with patch.object(helper, "discovery", return_value=("https://identity.test/auth", "https://identity.test/token")), patch.object(helper.webbrowser, "open", side_effect=browser), patch.object(helper, "request_json", side_effect=token_request):
            with self.assertRaisesRegex(ValueError, "did not grant"):
                helper.run(args)
            granted["scope"] = args.scope
            self.assertEqual(helper.run(args), ("synthetic-token", 300))
            granted["token_type"] = 7
            with self.assertRaisesRegex(ValueError, "bearer access token"):
                helper.run(args)

    def test_timeout_does_not_exchange_code(self):
        import argparse
        import socket
        s = socket.socket()
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
        s.close()
        args = argparse.Namespace(issuer="https://identity.test/realm", client_id="public-client", port=port,
                                  scope="openid data-fabric.query", resource=None)
        with patch.object(helper, "discovery", return_value=("https://identity.test/auth", "https://identity.test/token")), patch.object(helper.webbrowser, "open", return_value=True), patch.object(helper.time, "monotonic", side_effect=[0, 181]), patch.object(helper, "request_json") as request:
            with self.assertRaisesRegex(ValueError, "expired or was denied"):
                helper.run(args)
            request.assert_not_called()
    def test_slow_loopback_peer_has_bounded_read(self):
        import socket
        server = helper.LoopbackServer(("127.0.0.1", 0), helper.callback_handler("state", 0, {}))
        server.timeout = 1
        conn = socket.create_connection(server.server_address, timeout=2)
        conn.sendall(b"GET /callback HTTP/1.1\r\nHost:")
        thread = threading.Thread(target=server.handle_request, daemon=True)
        thread.start()
        thread.join(4)
        try:
            self.assertFalse(thread.is_alive(), "incomplete headers must not hang indefinitely")
        finally:
            conn.close()
            server.server_close()

if __name__ == "__main__":
    unittest.main()
