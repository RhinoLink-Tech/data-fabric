#!/usr/bin/env python3
"""Get a short-lived OIDC access token for a manually configured MCP client.

Requires an existing public client registered for http://127.0.0.1:PORT/callback.
This helper does not register clients, persist credentials, or refresh tokens.
"""
import argparse
import base64
import hashlib
import http.server
import json
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import webbrowser

MAX_RESPONSE = 1024 * 1024


def https_url(value):
    if not isinstance(value, str):
        raise ValueError("Expected an HTTPS URL")
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.fragment:
        raise ValueError("Expected an HTTPS URL without userinfo or fragment")
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("The identity provider returned an unexpected redirect")


def request_json(url, data=None):
    https_url(url)
    headers = {"Accept": "application/json"}
    if data is not None:
        data = urllib.parse.urlencode(data).encode("ascii")
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    request = urllib.request.Request(url, data=data, headers=headers)
    try:
        with urllib.request.build_opener(NoRedirect()).open(request, timeout=15) as response:
            body = response.read(MAX_RESPONSE + 1)
            if len(body) > MAX_RESPONSE:
                raise ValueError("Identity provider response is too large")
            return json.loads(body)
    except (urllib.error.URLError, OSError, UnicodeError, json.JSONDecodeError):
        raise ValueError("Identity provider request failed; check its URL, certificate, and client configuration") from None


def discovery(issuer):
    https_url(issuer)
    if urllib.parse.urlsplit(issuer).query:
        raise ValueError("Issuer cannot contain a query")
    metadata = request_json(issuer.rstrip("/") + "/.well-known/openid-configuration")
    if not isinstance(metadata, dict) or metadata.get("issuer") != issuer:
        raise ValueError("Discovery issuer does not match the configured issuer")
    return https_url(metadata.get("authorization_endpoint", "")), https_url(metadata.get("token_endpoint", ""))


def pkce():
    verifier = secrets.token_urlsafe(48)
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    challenge = base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")
    return verifier, challenge


def callback_handler(expected_state, port, result):
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, format, *args):
            pass

        def do_GET(self):
            target = urllib.parse.urlsplit(self.path)
            args = urllib.parse.parse_qs(target.query, keep_blank_values=True)
            valid_host = self.headers.get_all("Host", []) == [f"127.0.0.1:{port}"]
            valid_state = len(args.get("state", [])) == 1 and args["state"][0].isascii() and secrets.compare_digest(args["state"][0], expected_state)
            if target.path != "/callback" or not valid_host or not valid_state:
                self.send_error(400, "Invalid callback")
                return
            codes = args.get("code", [])
            errors = args.get("error", [])
            if len(codes) == 1 and codes[0] and not errors and len(codes[0]) <= 8192:
                result["code"] = codes[0]
                message = b"Authorization received. You can close this tab."
                status = 200
            elif len(errors) == 1 and not codes:
                result["error"] = True
                message = b"Authorization was not completed. You can close this tab."
                status = 400
            else:
                self.send_error(400, "Invalid callback")
                return
            self.send_response(status)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(message)))
            self.end_headers()
            self.wfile.write(message)

    return Handler


class LoopbackServer(http.server.ThreadingHTTPServer):
    daemon_threads = True
    block_on_close = False
    def handle_error(self, request, client_address):
        pass

    def get_request(self):
        connection, address = super().get_request()
        connection.settimeout(2)
        return connection, address


def run(args):
    authorization_endpoint, token_endpoint = discovery(args.issuer)
    if not 1024 <= args.port <= 65535:
        raise ValueError("Port must be between 1024 and 65535")
    redirect_uri = f"http://127.0.0.1:{args.port}/callback"
    verifier, challenge = pkce()
    state = secrets.token_urlsafe(32)
    result = {}
    params = {"response_type": "code", "client_id": args.client_id, "redirect_uri": redirect_uri,
              "scope": args.scope, "state": state, "code_challenge": challenge, "code_challenge_method": "S256"}
    if args.resource:
        params["resource"] = https_url(args.resource)
    # Existing endpoint query parameters are retained without overriding OAuth parameters.
    ep = urllib.parse.urlsplit(authorization_endpoint)
    existing = urllib.parse.parse_qsl(ep.query, keep_blank_values=True)
    if any(k in params for k, _ in existing):
        raise ValueError("Authorization endpoint contains conflicting query parameters")
    url = urllib.parse.urlunsplit((ep.scheme, ep.netloc, ep.path, urllib.parse.urlencode(existing + list(params.items())), ""))
    server = LoopbackServer(("127.0.0.1", args.port), callback_handler(state, args.port, result))
    server.timeout = 1
    try:
        print("Opening your identity provider in a browser. The callback listens only on 127.0.0.1.", file=sys.stderr)
        if not webbrowser.open(url):
            raise ValueError("Could not open the browser; verify your desktop browser configuration")
        deadline = time.monotonic() + 180
        while not result and time.monotonic() < deadline:
            server.handle_request()
    finally:
        server.server_close()
    if not result or result.get("error"):
        raise ValueError("Authorization expired or was denied")
    data = {"grant_type": "authorization_code", "client_id": args.client_id, "code": result["code"],
            "redirect_uri": redirect_uri, "code_verifier": verifier}
    if args.resource:
        data["resource"] = args.resource
    token = request_json(token_endpoint, data)
    if not isinstance(token, dict) or not isinstance(token.get("token_type"), str) or token["token_type"].lower() != "bearer" or not isinstance(token.get("access_token"), str) or not token["access_token"]:
        raise ValueError("Identity provider did not return a bearer access token")
    if "scope" in token and (not isinstance(token["scope"], str) or not set(args.scope.split()).issubset(set(token["scope"].split()))):
        raise ValueError("Identity provider did not grant all requested scopes")
    if any(ord(ch) < 33 or ord(ch) > 126 for ch in token["access_token"]):
        raise ValueError("Identity provider returned an invalid access token")
    return token["access_token"], token.get("expires_in")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--issuer", required=True, help="HTTPS OIDC issuer (must match provider metadata)")
    parser.add_argument("--client-id", required=True, help="Pre-registered public OAuth client ID")
    parser.add_argument("--port", required=True, type=int, help="Pre-registered loopback callback port")
    parser.add_argument("--scope", default="openid data-fabric.query", help="Requested scopes; management requires explicit grant and scope")
    parser.add_argument("--resource", help="Optional HTTPS OAuth resource identifier")
    args = parser.parse_args()
    if not sys.stdin.isatty() or not sys.stdout.isatty():
        parser.error("Run interactively in a trusted terminal; token output cannot be redirected")
    try:
        token, expiry = run(args)
        print(f"Access token obtained; expires_in={expiry if isinstance(expiry, int) else 'unknown'} seconds.", file=sys.stderr)
        input("The next line is sensitive. Ensure terminal history/recording is private, then press Enter to display it: ")
        print(token)
        print("Copy into WorkBuddy's credential field. Do not paste into chat. Reauthorize after expiry.", file=sys.stderr)
    except (ValueError, OSError, KeyboardInterrupt, EOFError) as exc:
        print(f"Authorization did not complete: {exc if isinstance(exc, ValueError) else 'local error or interruption'}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
