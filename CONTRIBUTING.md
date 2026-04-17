# Contributing to kuma-migrator

Thank you for your interest in contributing! This document explains how to report
issues, propose changes, and submit pull requests.

By contributing you agree that your work will be released under the
[Apache License 2.0](LICENSE) that covers this project.

---

## Table of Contents

1. [Code of Conduct](#code-of-conduct)
2. [Reporting Bugs](#reporting-bugs)
3. [Suggesting Enhancements](#suggesting-enhancements)
4. [Development Setup](#development-setup)
5. [Making Changes](#making-changes)
6. [Commit Messages](#commit-messages)
7. [Pull Requests](#pull-requests)
8. [License](#license)

---

## Code of Conduct

This project follows the
[Contributor Covenant Code of Conduct](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).
Please be respectful and constructive in all interactions.

---

## Reporting Bugs

Before opening an issue, search
[existing issues](https://github.com/Kong/kuma-migrator/issues) to avoid
duplicates.

When filing a bug report please include:

- **kuma-migrator version** (`kuma-migrator version`)
- **Kuma / Kong Mesh version** you are migrating from
- **Command you ran** (redact any sensitive resource names if needed)
- **Expected behaviour**
- **Actual behaviour** — include the full output or error message
- A **minimal reproducer**: a trimmed YAML file that triggers the issue

---

## Suggesting Enhancements

Open an issue with the prefix `[feature]` in the title. Describe:

- The migration scenario or use case that is not covered
- The Kuma / Kong Mesh version where the new API was introduced
- A before/after YAML example if applicable

---

## Development Setup

**Requirements**: Go 1.24+, `kubectl`, `kumactl` (for manual extract testing)

```bash
git clone https://github.com/Kong/kuma-migrator.git
cd kuma-migrator
go mod download
make test    # run the full test suite
make build   # compile to ./dist/kuma-migrator
```

### Useful make targets

| Target | Description |
|---|---|
| `make build` | Compile binary to `./dist/kuma-migrator` |
| `make test` | Run all unit tests |
| `make lint` | Run `golangci-lint` |
| `make snapshot` | Local GoReleaser dry-run (requires `goreleaser`) |
| `make clean` | Remove `./dist/` |

---

## Making Changes

### Code layout

```
cmd/           # Cobra CLI commands (thin wrappers, no business logic)
pkg/
  config/      # User config file loading
  extractor/   # extract command: CP mode detection, kubectl/kumactl wrappers
  migrator/    # Core migration engine: detection, transformation, reporting
  resource/    # Kind → subfolder mapping
```

### Migration scenarios

New migration scenarios follow a consistent pattern — please keep it:

1. Add a constant to `pkg/migrator/types.go`
2. Add detection logic to `pkg/migrator/detect.go`
3. Implement the transform in `pkg/migrator/<scenario>.go`
4. Wire it into `pkg/migrator/transform.go` and `pkg/migrator/migrator.go`
5. Add tests alongside the implementation (`<scenario>_test.go`)

Deprecation warnings (no auto-fix) belong in `pkg/migrator/deprecation.go` and
are called as a post-pass on every output document.

### Tests

- Every transform function must have unit tests covering at least: happy path,
  edge cases (empty fields, missing fields), and error cases.
- Tests use only the standard library and `sigs.k8s.io/yaml` — no test framework.
- Run `go test ./...` before pushing; CI will reject failing tests.

---

## Commit Messages

Follow the [Conventional Commits](https://www.conventionalcommits.org/) format:

```
<type>: <short summary in imperative mood>

<optional body>
```

Types: `feat`, `fix`, `docs`, `refactor`, `test`, `ci`, `chore`

Examples:
```
feat: add MeshPassthrough extraction from Mesh CRD
fix: skip MeshService resources with kuma.io/env: kubernetes on extract
docs: document all-zones output directory in README
```

- Keep the subject line under 72 characters
- Use the body to explain *why*, not *what*
- Reference issues with `Fixes #123` or `Closes #123`

---

## Pull Requests

1. **Fork** the repository and create a branch from `main`:
   ```bash
   git checkout -b feat/my-feature
   ```
2. Make your changes following the guidelines above.
3. Ensure `make test` and `make lint` pass locally.
4. Open a pull request against `main` with:
   - A clear title (same format as commit messages)
   - A description of what changed and why
   - A before/after YAML example for migration changes
   - Reference to any related issue
5. Address review feedback; squash fixup commits before merge.

CI runs on every PR: tests, vet, license check, cross-compilation for all five
target platforms (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64,
windows/amd64), and goreleaser config validation.

---

## License

By submitting a pull request you confirm that:

- You authored the contribution, or have the right to submit it.
- You agree to license your contribution under the
  [Apache License 2.0](LICENSE).

There is no CLA — the Apache 2.0 license terms are sufficient.
