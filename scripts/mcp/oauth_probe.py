#!/usr/bin/env python3
"""Check public MCP OAuth discovery without sending credentials."""
import argparse
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

from onboard import issuer_url_has_valid_syntax

MAX_METADATA_BYTES = 65536
TIMEOUT_SECONDS = 5


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def validate_url(value, *, resource=False):
    try:
        parsed = urllib.parse.urlsplit(value)
        valid = (
            value == value.strip() and parsed.scheme == "https"
            and bool(parsed.hostname) and parsed.username is None
            and parsed.password is None and not parsed.query and not parsed.fragment
            and "?" not in value and "#" not in value
            and parsed.geturl() == value and issuer_url_has_valid_syntax(value, parsed)
        )
        if resource:
            valid = valid and parsed.path == "/mcp"
        if not valid:
            raise ValueError()
        return parsed
    except ValueError:
        raise ValueError("URL must be canonical HTTPS without credentials, query or fragment"
                         + (" and end in /mcp" if resource else "")) from None


def request(opener, url):
    req = urllib.request.Request(url, headers={"Accept": "application/json",
                                               "User-Agent": "data-fabric-oauth-probe/1"})
    try:
        with opener.open(req, timeout=TIMEOUT_SECONDS) as response:
            data = response.read(MAX_METADATA_BYTES + 1)
            return response.status, response.headers, data
    except urllib.error.HTTPError as error:
        try:
            return error.code, error.headers, b""
        finally:
            error.close()
    except (OSError, ValueError, urllib.error.URLError):
        return None, {}, b""


def check_metadata(opener, url, resource, issuer):
    status, _, data = request(opener, url)
    if status != 200:
        return False, f"HTTP {status}" if status is not None else "network or TLS error"
    if len(data) > MAX_METADATA_BYTES:
        return False, "metadata response is too large"
    try:
        obj = json.loads(data)
    except (ValueError, UnicodeError):
        return False, "metadata is not valid JSON"
    if not isinstance(obj, dict) or obj.get("resource") != resource:
        return False, "metadata resource does not match"
    servers = obj.get("authorization_servers")
    if not isinstance(servers, list) or issuer not in servers:
        return False, "metadata authorization server does not match"
    return True, "resource and authorization server match"


def check_challenge(opener, resource, metadata_url):
    status, headers, _ = request(opener, resource)
    if status != 401:
        return False, f"HTTP {status}, expected 401" if status is not None else "network or TLS error"
    challenge = headers.get("WWW-Authenticate", "")
    # Restrict this diagnostic to the Bearer challenge and the exact canonical URL.
    match = re.search(r'(?i)^Bearer\s+.*?resource_metadata\s*=\s*"([^"]+)"', challenge)
    if not match or match.group(1) != metadata_url:
        return False, "Bearer challenge does not advertise the expected metadata URL"
    return True, "Bearer challenge advertises the metadata URL"


def probe(resource, issuer, opener=None):
    parsed = validate_url(resource, resource=True)
    validate_url(issuer)
    origin = urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, "", "", ""))
    path_metadata = origin + "/.well-known/oauth-protected-resource/mcp"
    root_metadata = origin + "/.well-known/oauth-protected-resource"
    opener = opener or urllib.request.build_opener(NoRedirect())
    results = []
    for name, url in (("path metadata", path_metadata), ("root metadata", root_metadata)):
        ok, message = check_metadata(opener, url, resource, issuer)
        results.append((ok, name, message))
    ok, message = check_challenge(opener, resource, path_metadata)
    results.append((ok, "MCP challenge", message))
    return results


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--resource", required=True, help="public HTTPS MCP URL ending in /mcp")
    parser.add_argument("--issuer", required=True, help="exact HTTPS authorization server issuer")
    args = parser.parse_args(argv)
    try:
        results = probe(args.resource, args.issuer)
    except ValueError as exc:
        print("ERR " + str(exc), file=sys.stderr)
        return 2
    for ok, name, message in results:
        print(("OK  " if ok else "ERR ") + name + ": " + message)
    print("Discovery only: login, issued tokens, refresh, permissions and client use were not tested.")
    return 0 if all(ok for ok, _, _ in results) else 1


if __name__ == "__main__":
    sys.exit(main())
