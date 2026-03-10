# Contributing to gnata

Thank you for your interest in contributing to gnata! This document covers the process for contributing to this project.

## Getting Started

1. Fork the repository and clone your fork
2. Create a feature branch from `main`
3. Make your changes
4. Run tests and linting (see below)
5. Open a pull request

## Development

### Prerequisites

- Go 1.25.1 or later
- [golangci-lint](https://golangci-lint.run/welcome/install/)

### Running Tests

```bash
go test ./...
```

### Running Linter

```bash
golangci-lint run
```

### Running Benchmarks

```bash
go test -bench=. -benchmem
```

## Pull Requests

- Keep PRs focused on a single change
- Include tests for new functionality
- Ensure all existing tests pass
- Run the linter before submitting

### Commit Messages

Use clear, descriptive commit messages. Start with a short summary (under 50 characters), followed by a blank line and a more detailed description if needed.

### Sign-Off (DCO)

All commits must be signed off to certify that you have the right to submit the contribution under the project's license. Add a sign-off line to your commits:

```
Signed-off-by: Your Name <your.email@example.com>
```

You can do this automatically with `git commit -s`.

This certifies that you agree to the [Developer Certificate of Origin](https://developercertificate.org/).

## Reporting Issues

- Use GitHub Issues for bug reports and feature requests
- Include a minimal reproducing example for bugs
- Describe expected vs actual behavior

## Code Style

- Follow standard Go conventions (`gofmt`, `goimports`)
- Use descriptive variable names
- Keep functions focused and under 100 lines where practical
- Handle all errors explicitly

## AI-Assisted Contributions

We welcome AI-assisted contributions. If you used AI tools (Copilot, Cursor, Claude, etc.) to generate substantial portions of your submission, please note this in your PR description.

Regardless of how code was produced, the contributor is responsible for its correctness, test coverage, and adherence to the project's style and quality standards. All contributions receive the same review process.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
