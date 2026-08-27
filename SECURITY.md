# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.0.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability, please report it by emailing **security@ansvar.eu**.

Do **not** file a public GitHub issue for security vulnerabilities.

We will acknowledge receipt within 48 hours and aim to provide a fix within 7 days for critical issues.

## Scope

This server provides read-only access to Hungarian legislation. It does not:
- Accept user-uploaded files
- Execute user-provided code
- Store user data
- Require authentication (public endpoint)

The primary security concerns are:
- **Supply chain**: Dependencies scanned via Trivy (on PRs, pushes to `main`, and daily); `npm audit` runs in the publish workflow
- **Static analysis**: Semgrep scans pull requests and pushes to `main`
- **Posture**: OpenSSF Scorecard checks run automatically
