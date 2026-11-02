---
title: Installation
description: Install kuma-migrator with Homebrew, from a release archive, or from source, and verify a release archive.
order: 10
---

## Homebrew

Supported platforms: macOS Apple Silicon (`arm64`), macOS Intel (`amd64`), Linux `amd64`,
Linux `arm64`.

```bash
brew tap bcollard/kuma-migrator
brew install --cask kuma-migrator
```

Or in one command:

```bash
brew install --cask bcollard/kuma-migrator/kuma-migrator
```

Upgrade:

```bash
brew upgrade --cask kuma-migrator
```

## Release archives

Download the archive for your platform from
[GitHub Releases](https://github.com/Kong/kuma-migrator/releases).

| Platform | Architecture | Archive |
|---|---|---|
| Linux | `amd64`, `arm64` | `kuma-migrator_<version>_linux_<arch>.tar.gz` |
| macOS | `amd64` (Intel), `arm64` (Apple Silicon) | `kuma-migrator_<version>_darwin_<arch>.tar.gz` |
| Windows | `amd64` | `kuma-migrator_<version>_windows_amd64.zip` |

Linux `amd64`:

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -L "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_amd64.tar.gz" | tar xz
sudo mv kuma-migrator /usr/local/bin/
```

Linux `arm64`:

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -L "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_arm64.tar.gz" | tar xz
sudo mv kuma-migrator /usr/local/bin/
```

## From source

Requires Go 1.24 or later.

```bash
git clone https://github.com/Kong/kuma-migrator.git
cd kuma-migrator
make build
# binary at ./dist/kuma-migrator
```

## Verify a release archive

Every release archive carries a
[build provenance attestation](https://github.com/Kong/kuma-migrator/attestations). It binds
the archive to the commit and the GitHub Actions run that built it. Check it with the
[GitHub CLI](https://cli.github.com/) (`gh attestation verify` is built in):

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -LO "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_amd64.tar.gz"
gh attestation verify "kuma-migrator_${VERSION}_linux_amd64.tar.gz" --repo Kong/kuma-migrator
```

The Homebrew cask is built from the same archives, and its `sha256` fields match the attested
`checksums.txt`.

## Runtime requirements

| Tool | Needed for |
|---|---|
| `kubectl` on `PATH` | `extract --kube-context`, and applying Kubernetes-format output |
| `kumactl` on `PATH` | `extract --kumactl-context` against a self-hosted control plane, and applying with `kumactl` |

`migrate` needs neither: it reads and writes local files only.

For a Kong Mesh control plane, use the Kong Mesh build of `kumactl`. The Kuma OSS binary
refuses to talk to a Kong Mesh control plane. Extraction from Konnect uses direct HTTPS calls
and only reads the context from the `kumactl` config file.

Check the installed version:

```bash
kuma-migrator version
```

Next: [Typical usage](usage.md).
