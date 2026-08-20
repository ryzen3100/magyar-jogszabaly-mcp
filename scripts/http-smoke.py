#!/usr/bin/env python3
"""Minimal MCP HTTP smoke test; OAuth is optional via --oauth."""

from __future__ import annotations

import base64
import hashlib
import json
import os
import secrets
import sys
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, new_url):
        return None


OPENER = urllib.request.build_opener(NoRedirect())


def request(base: str, path: str, *, method: str = "GET", data: bytes | None = None, headers: dict[str, str] | None = None):
    req = urllib.request.Request(
        base + path,
        data=data,
        method=method,
        headers=headers or {},
    )
    try:
        return OPENER.open(req, timeout=30)
    except urllib.error.HTTPError as error:
        return error


def response_body(response) -> str:
    body = response.read().decode()
    if body.lstrip().startswith("{"):
        return body
    for line in body.splitlines():
        if line.startswith("data: "):
            return line[6:]
    return body


def require_status(response, expected: int, label: str) -> None:
    if response.status != expected:
        raise RuntimeError(f"{label} failed with HTTP {response.status}: {response_body(response)}")


def main() -> None:
    base = (sys.argv[1] if len(sys.argv) > 1 else os.environ.get("HTTP_SMOKE_URL", "http://127.0.0.1:3000")).rstrip("/")
    oauth = "--oauth" in sys.argv[2:]
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
    }

    if oauth:
        redirect_uri = "http://127.0.0.1/callback"
        registered = request(
            base,
            "/oauth/register",
            method="POST",
            data=json.dumps({"redirect_uris": [redirect_uri]}).encode(),
            headers={"Content-Type": "application/json"},
        )
        require_status(registered, 200, "client registration")
        client_id = json.loads(response_body(registered))["client_id"]

        verifier = secrets.token_urlsafe(32)
        challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
        query = urllib.parse.urlencode({
            "client_id": client_id,
            "redirect_uri": redirect_uri,
            "response_type": "code",
            "code_challenge": challenge,
            "code_challenge_method": "S256",
        })
        authorized = request(base, "/oauth/authorize?" + query)
        if authorized.status != 302:
            raise RuntimeError(f"authorization failed with HTTP {authorized.status}: {response_body(authorized)}")
        location = authorized.headers.get("Location")
        if not location:
            raise RuntimeError("authorization did not return a redirect location")
        code = urllib.parse.parse_qs(urllib.parse.urlparse(location).query)["code"][0]

        token = request(
            base,
            "/oauth/token",
            method="POST",
            data=urllib.parse.urlencode({
                "grant_type": "authorization_code",
                "code": code,
                "code_verifier": verifier,
                "client_id": client_id,
            }).encode(),
            headers={"Content-Type": "application/x-www-form-urlencoded"},
        )
        require_status(token, 200, "token exchange")
        access_token = json.loads(response_body(token))["access_token"]
        headers["Authorization"] = f"Bearer {access_token}"
    else:
        metadata = request(base, "/.well-known/oauth-protected-resource")
        require_status(metadata, 404, "disabled OAuth metadata")
    initialize = request(
        base,
        "/mcp",
        method="POST",
        data=json.dumps({
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "protocolVersion": "2025-03-26",
                "capabilities": {},
                "clientInfo": {"name": "container-smoke-test", "version": "1.0.0"},
            },
        }).encode(),
        headers=headers,
    )
    require_status(initialize, 200, "MCP initialize")
    if "result" not in json.loads(response_body(initialize)):
        raise RuntimeError("MCP initialize returned no result")

    session_id = initialize.headers.get("mcp-session-id")
    if not session_id:
        raise RuntimeError("MCP initialize did not return mcp-session-id")
    headers["mcp-session-id"] = session_id

    search = request(
        base,
        "/mcp",
        method="POST",
        data=json.dumps({
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/call",
            "params": {
                "name": "search_legislation",
                "arguments": {"query": "privacy", "limit": 1},
            },
        }).encode(),
        headers=headers,
    )
    require_status(search, 200, "search_legislation")
    result = json.loads(response_body(search))
    content = result.get("result", {}).get("content", [])
    if "error" in result or not content:
        raise RuntimeError(f"search_legislation returned no usable result: {result}")

    print("MCP HTTP smoke passed: initialize + search_legislation")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"MCP HTTP smoke failed: {error}", file=sys.stderr)
        sys.exit(1)
