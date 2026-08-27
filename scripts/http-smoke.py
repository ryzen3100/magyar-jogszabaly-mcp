#!/usr/bin/env python3
"""Minimal MCP HTTP smoke test (server routes: /health, /mcp, /icon.png, server card)."""

from __future__ import annotations

import json
import sys
import urllib.error
import urllib.request


def request(base: str, path: str, *, method: str = "GET", data: bytes | None = None, headers: dict[str, str] | None = None):
    req = urllib.request.Request(
        base + path,
        data=data,
        method=method,
        headers=headers or {},
    )
    try:
        return urllib.request.urlopen(req, timeout=30)
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
    base = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:3000").rstrip("/")
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
    }

    # Regression guard: OAuth metadata must stay absent (no auth on this server).
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
