BINARY     := kuma-migrator
MODULE     := github.com/Kong/kuma-migrator
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -ldflags "-s -w -X main.version=$(VERSION)"
BUILD_DIR  := ./dist

.PHONY: build test lint install clean tidy snapshot release-check site site-check site-test

build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) .

test:
	go test ./...

lint:
	golangci-lint run ./...

install:
	go install $(LDFLAGS) .

clean:
	rm -rf $(BUILD_DIR)

tidy:
	go mod tidy

# Local dry-run release (no publish). Requires goreleaser.
snapshot:
	goreleaser release --snapshot --clean

# Validate .goreleaser.yaml
release-check:
	goreleaser check

# Documentation site: regenerate docs/docs/ and docs/sitemap.xml from site/.
# The generator is a separate Go module (site/gen) so it never touches the CLI's
# dependency graph. Pass SITE_LASTMOD=YYYY-MM-DD to pin the sitemap date.
SITE_LASTMOD ?=

site:
	cd site/gen && go run . -root ../.. $(if $(SITE_LASTMOD),-lastmod $(SITE_LASTMOD))

site-test:
	cd site/gen && go vet ./... && go test ./...

# Fails when the committed output differs from a fresh build. Two kinds of line are
# ignored because they come from git, not from site/: <lastmod> (moves with every
# commit touching site/) and the header's release-tag badge (moves when a tag is
# pushed, and is empty in a shallow clone). docs/index.html is checked because the
# generator rewrites its release-tag, shared.css and copy.js regions.
site-check: site
	git diff --exit-code -I '<lastmod>' -I '<!-- release-tag -->' -- docs/docs docs/sitemap.xml docs/index.html
	@untracked=$$(git ls-files --others --exclude-standard -- docs/docs); \
	if [ -n "$$untracked" ]; then \
		echo "site-check: generated files are not committed:"; echo "$$untracked"; exit 1; \
	fi
