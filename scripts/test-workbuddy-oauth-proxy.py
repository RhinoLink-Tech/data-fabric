#!/usr/bin/env python3
"""Disposable Nginx transport contract; requires Docker, OpenSSL and cached images.

No deployed service or credential is used. The upstream is a stub: these tests
verify proxy routing, while Go tests cover the actual discovery implementation.
"""
import http.client
import pathlib
import subprocess
import tempfile
import time
import unittest
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
NAME = "df-oauth-proxy-test-" + uuid.uuid4().hex[:12]
NETWORK = NAME
BACKEND = NAME + "-gateway"
PROXY = NAME + "-nginx"
HOST = "fabric.example"
PATHS = ("/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp")
STUB = r'''
from http.server import BaseHTTPRequestHandler, HTTPServer
class Handler(BaseHTTPRequestHandler):
 def log_message(self, *args): pass
 def do_GET(self):
  if self.path.startswith("/.well-known/oauth-protected-resource"):
   self.send_response(200 if self.headers.get("X-Test-Enabled") == "yes" else 404)
   self.end_headers(); self.wfile.write(self.path.encode()); return
  if self.path == "/mcp":
   self.send_response(200 if self.headers.get("Authorization") == "Bearer synthetic" else 401)
   if self.headers.get("X-Test-Enabled") == "yes":
    self.send_header("WWW-Authenticate", 'Bearer resource_metadata="https://fabric.example/.well-known/oauth-protected-resource/mcp"')
   self.end_headers(); self.wfile.write(b"gateway"); return
  self.send_response(200); self.end_headers(); self.wfile.write(b"unexpected gateway route")
HTTPServer(("0.0.0.0", 8082), Handler).serve_forever()
'''


def run(*args):
 return subprocess.run(args, check=True, capture_output=True, text=True)

class ProxyContract(unittest.TestCase):
 @classmethod
 def setUpClass(cls):
  cls.tmp = tempfile.TemporaryDirectory(prefix="df-oauth-proxy-")
  work = pathlib.Path(cls.tmp.name)
  try:
   run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=fabric.example", "-keyout", str(work / "key.pem"), "-out", str(work / "cert.pem"))
   config = (ROOT / "deploy/mcp/workbuddy-identity-proxy.conf.template").read_text()
   config = config.replace("${DF_PUBLIC_HOST}", HOST).replace("${DF_SIGNUP_CHALLENGE}", "synthetic").replace("${DF_SIGNUP_STATE}", "synthetic")
   config = config.replace("/etc/df-proxy/tls.crt", "/work/cert.pem").replace("/etc/df-proxy/tls.key", "/work/key.pem")
   (work / "nginx.conf").write_text("events {}\nhttp {\n" + config + "\n}\n")
   run("docker", "network", "create", NETWORK)
   run("docker", "run", "-d", "--name", BACKEND, "--network", NETWORK, "--network-alias", "gateway", "python:3.12-slim", "python3", "-c", STUB)
   run("docker", "run", "-d", "--name", PROXY, "--network", NETWORK, "-p", "127.0.0.1::8080", "-v", str(work) + ":/work:ro", "-v", str(work / "nginx.conf") + ":/etc/nginx/nginx.conf:ro", "nginx:1.27-alpine")
   cls.port = int(run("docker", "port", PROXY, "8080/tcp").stdout.strip().rsplit(":", 1)[1])
   for attempt in range(30):
    try:
     cls.request("/mcp"); break
    except (ConnectionError, OSError): time.sleep(.1)
   else: raise RuntimeError("proxy did not become reachable")
  except Exception:
   cls.cleanup(); raise

 @classmethod
 def cleanup(cls):
  for name in (PROXY, BACKEND): subprocess.run(["docker", "rm", "-f", name], capture_output=True)
  subprocess.run(["docker", "network", "rm", NETWORK], capture_output=True)
  cls.tmp.cleanup()

 @classmethod
 def tearDownClass(cls): cls.cleanup()

 @classmethod
 def request(cls, path, headers=None):
  conn = http.client.HTTPConnection("127.0.0.1", cls.port, timeout=4)
  try:
   conn.request("GET", path, headers={"Host": HOST, **(headers or {})})
   response = conn.getresponse(); body = response.read().decode()
   return response.status, dict(response.getheaders()), body
  finally: conn.close()

 def test_discovery_enabled_disabled_and_exact_paths(self):
  for path in PATHS:
   with self.subTest(path=path):
    status, _, body = self.request(path, {"X-Test-Enabled": "yes"})
    self.assertEqual((status, body), (200, path))
    self.assertEqual(self.request(path)[0], 404)
  for path in ("/.well-known/oauth-protected-resource/other", "/api/v1/datasources", "/internal/replica", "/health"):
   self.assertEqual(self.request(path, {"X-Test-Enabled": "yes"})[0], 404, path)

 def test_mcp_auth_and_host_policy_preserved(self):
  status, headers, _ = self.request("/mcp", {"X-Test-Enabled": "yes"})
  self.assertEqual(status, 401)
  self.assertIn("resource_metadata=", headers.get("WWW-Authenticate", ""))
  self.assertEqual(self.request("/mcp", {"Authorization": "Bearer synthetic"})[0], 200)
  self.assertEqual(self.request(PATHS[0], {"Host": "attacker.example", "X-Test-Enabled": "yes"})[0], 421)

if __name__ == "__main__": unittest.main()
