# Security Setup Guide (Internal)

## Required Secrets

No repository secrets are required. The only credential used is the built-in `GITHUB_TOKEN` (GHCR image push in `docker-publish.yml`).

The npm package (`@ansvar/hungarian-law-mcp`) is no longer published as of the Go port (v2.0.0); the `NPM_TOKEN` secret can be removed from repository settings.

## Release Publishing

- `.github/workflows/publish.yml`: a `v*` tag push runs `go vet`, `go test`, and `go build` as a release gate.
- `.github/workflows/docker-publish.yml`: builds the multi-stage Go image and pushes it to GHCR (`ghcr.io/ryzen3100/magyar-jogszabaly-mcp`), then smoke-tests it with `scripts/http-smoke.py`.

MCP registry submission (`server.json`) is currently a manual step — see `server.json` for the registry metadata.

## Branch Protection

Enable these rules on `main`:
- Require pull request reviews (1 reviewer)
- Require status checks to pass (`test` from ci.yml, `publish` from publish.yml)
- Require branches to be up to date
- Do not allow bypassing the above settings

## Security Scanning

Scanners live in separate workflow files under `.github/workflows/`:
- **Semgrep** (pattern SAST) — `semgrep.yml`, on PRs and pushes to `main`
- **Trivy** (dependency CVE + container scan) — `trivy.yml`, on PRs, pushes to `main`, and daily schedule
- **OSSF Scorecard** (security posture) — `scorecard.yml`
