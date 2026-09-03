# Installation

Homebrew, pre-built binaries, and building from source.

[← Back to the README](../README.md)

---

## Homebrew (macOS and Linux)

Supported platforms: macOS Apple Silicon (`arm64`), macOS Intel (`amd64`),
Linux `amd64`, Linux `arm64`.

```bash
brew tap bcollard/kuma-migrator
brew install --cask kuma-migrator
```

Or as a one-liner:

```bash
brew install --cask bcollard/kuma-migrator/kuma-migrator
```

Upgrade to the latest version at any time:

```bash
brew upgrade --cask kuma-migrator
```

## Pre-built binaries

Download the binary for your platform from the
[GitHub Releases](https://github.com/Kong/kuma-migrator/releases) page.
Archives are provided for:

| Platform | Architecture |
|---|---|
| Linux | `amd64`, `arm64` |
| macOS | `amd64` (Intel), `arm64` (Apple Silicon) |
| Windows | `amd64` |

**Linux (amd64):**

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -L "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_amd64.tar.gz" | tar xz
sudo mv kuma-migrator /usr/local/bin/
```

**Linux (arm64):**

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -L "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_arm64.tar.gz" | tar xz
sudo mv kuma-migrator /usr/local/bin/
```

## From source

```bash
git clone https://github.com/Kong/kuma-migrator.git
cd kuma-migrator
make build
# binary at ./dist/kuma-migrator
```

## Verifying a release

Every release archive (and the `homebrew_casks` formula built from it) carries a
[build provenance attestation](https://github.com/Kong/kuma-migrator/attestations) — a
cryptographic record binding it to the exact commit and GitHub Actions run that built it. You do
not have to trust that a downloaded archive matches this repository; you can check:

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -LO "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_amd64.tar.gz"
gh attestation verify "kuma-migrator_${VERSION}_linux_amd64.tar.gz" --repo Kong/kuma-migrator
```

This requires the [GitHub CLI](https://cli.github.com/) — `gh attestation verify` is a built-in
subcommand, no extra plugin needed.
