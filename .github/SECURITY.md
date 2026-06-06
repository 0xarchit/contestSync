# Security Policy

## Supported Versions

Only the latest version of ContestSync is supported for security updates.

| Version | Supported          |
| ------- | ------------------ |
| 1.x.x   | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a Vulnerability

We take the security of our users' data very seriously. If you find a security vulnerability, please do NOT open a public issue.

Instead, please email mail@0xarchit.is-a.dev with the details. We will respond within 48 hours and work with you to resolve the issue.

### Core Security Mandates
- No sensitive data is ever stored in plaintext.
- All database queries are parameterized.
- CSRF and XSS protection are enforced at the middleware level.
