"""Serve only the discovery document and public keys for the reference profile."""

from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import ssl

DIRECTORY = Path("/reference/issuer")
DOCUMENTS = {
    "/.well-known/openid-configuration": "discovery.json",
    "/jwks": "jwks.json",
}


class FixtureHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        filename = DOCUMENTS.get(self.path)
        if filename is None:
            self.send_error(404)
            return
        data = (DIRECTORY / filename).read_bytes()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, _format, *_args):
        pass


if __name__ == "__main__":
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.minimum_version = ssl.TLSVersion.TLSv1_2
    tls.load_cert_chain(DIRECTORY / "tls.crt", DIRECTORY / "tls.key")
    with HTTPServer(("0.0.0.0", 8443), FixtureHandler) as server:
        server.socket = tls.wrap_socket(server.socket, server_side=True)
        server.serve_forever()
