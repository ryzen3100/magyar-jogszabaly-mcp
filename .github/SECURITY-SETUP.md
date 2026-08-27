# Security Setup Guide (Internal)

## Required Secrets

Configure these secrets in the GitHub repository settings:

| Secret | Purpose | Source |
|--------|---------|--------|
| `NPM_TOKEN` | npm publishing with provenance | npm.js account (Ansvar org) |

## npm Publishing

Publishing is automated by `.github/workflows/publish.yml`: a `v*` tag push runs lint, unit tests, contract tests, and build, then `npm publish --provenance`. npm package names in the `@ansvar` scope and provenance signing require the `NPM_TOKEN` secret and a verified npm account.

MCP registry submission (server.json / `mcpName`) is currently a manual step — see `server.json` for the registry metadata.

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
- **npm audit** — advisory run inside `publish.yml` before publishing
