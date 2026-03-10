# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in gnata, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@reco.ai** with:

- A description of the vulnerability
- Steps to reproduce the issue
- The potential impact
- Any suggested fix (if applicable)

## Response Timeline

- **Acknowledgment**: Within 2 business days
- **Initial assessment**: Within 5 business days
- **Fix or mitigation**: Depends on severity, but we aim for 30 days for critical issues

## Scope

The following are in scope:

- The gnata Go library (published as `github.com/recolabs/gnata`)
- Expression parsing and evaluation logic
- ReDoS or other denial-of-service vectors in expression evaluation
- Memory safety issues

The following are out of scope:

- The WASM playground (intended for demonstration only)
- Issues in dependencies (report those to the respective maintainers)

## Supported Versions

Security fixes are applied to the latest release only.
