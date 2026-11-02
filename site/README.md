# Documentation site sources

The `/docs` section of https://kong.github.io/kuma-migrator/ is generated from this directory.
GitHub Pages serves the repository's `docs/` folder as-is (`docs/.nojekyll`, no build step on
GitHub), so the generated HTML is committed.

```
make site        # regenerate docs/docs/ from site/
make site-check  # regenerate and fail if docs/docs/ differs from what is committed (CI)
make site-test   # unit tests of the generator
```

The build fails, listing every problem as `file:line: message`, on: a page without `title`, unknown
front matter keys, a dangling `.md`/`policy:`/`#anchor`/site-root link, a duplicate slug, a policy
entry with an unknown `group`/`product`/`status`, a `topics` slug with no topic page, a `code` path
that does not exist, a `docs/...` link on `docs/index.html` that matches no generated page, a code
fence without a language tag (or with one chroma does not know), or an indented code block.

The sitemap `<lastmod>` is the date of the last commit touching `site/` (today's date while `site/`
has uncommitted changes); `make site SITE_LASTMOD=YYYY-MM-DD` pins it. `site-check` ignores
`<lastmod>` lines.

### Release badge

The header of every docs page and of the landing page shows the latest release tag, linking to
`https://github.com/Kong/kuma-migrator/releases/latest`. The generator takes it from
`git describe --tags --abbrev=0` at build time (`go run . -release vX.Y.Z` overrides it,
`-release none` hides it) and writes it on a single line between `<!-- release-tag -->` and
`<!-- /release-tag -->`. No request goes to the GitHub API at runtime.

**After a release, run `make site` and commit** to refresh the badge. Until then the site shows
the previous tag. `site-check` ignores the release-tag line, so pushing a tag never fails CI, and
a shallow CI clone (no tags, empty badge) does not either.

### Landing page

`docs/index.html` is hand-written and self-contained (inline CSS and JS, no external requests). The
generator rewrites three marked regions of it on every build, so the landing page and the docs
share one header and one code palette:

| Region | Source |
|---|---|
| `/* shared.css: … */` … `/* /shared.css */` in `<style>` | `site/static/shared.css` (tokens, header, code blocks) |
| `/* copy.js: … */` … `/* /copy.js */` in `<script>` | `site/static/copy.js` (copy buttons) |
| `<!-- release-tag -->` … `<!-- /release-tag -->` | the release tag (above) |

Edit the files under `site/static/`, not the copies in `docs/index.html`. `site-check` diffs
`docs/index.html` too. Code blocks on the landing page are written by hand with the same classes
the generator emits (see below).

## Layout

```
site/
  content/
    _index.md               landing page of /docs/
    guides/_index.md        section landing page
    guides/<slug>.md        one page per guide          → /docs/guides/<slug>/
    topics/_index.md
    topics/<slug>.md        one page per topic          → /docs/topics/<slug>/
    policies/_index.md      intro prose for the policy index (the tables are generated)
  data/
    policies.yaml           one entry per resource kind → /docs/policies/ and /docs/policies/<kind>/
  templates/                HTML templates (generator-owned)
  static/                   CSS/JS/SVG copied to docs/docs/static/ (generator-owned);
                            shared.css and copy.js are also inlined into docs/index.html
  gen/                      the generator: separate Go module, not part of the CLI's module
```

Output goes to `docs/docs/`, using directory-style URLs (`docs/docs/topics/mtls-identity/index.html`).

## Page format

Markdown (CommonMark + GFM tables, fenced code with a language tag) with YAML front matter:

```markdown
---
title: mTLS and workload identity      # page <h1> and <title>
description: One sentence for <meta description> and the section index card.
order: 20                              # sort order inside the section (ascending)
nav_title: mTLS & identity             # optional, shorter label for the sidebar
aliases: [old-slug]                    # optional, former slugs in the same section
---
```

Do not repeat the title as a `# heading` in the body; start at `##`. Each `##` becomes an entry in
the on-page table of contents.

`aliases` keeps old links working after a rename: each former slug gets a redirect page
(`<meta http-equiv="refresh">`, `noindex`, the `#fragment` kept) that is left out of the
sitemap and the search index.

### Raw HTML

HTML blocks are passed through, for layout only. Wrap Markdown in a `<div class="…">` with a blank
line after the opening tag and before the closing one, so the Markdown inside is still parsed and
its links still checked. Links written in raw HTML are **not** checked; keep them in Markdown.
`guides/getting-started.md` uses this for its step cards (`story-step`, `story-art`, `kit`,
`legend`, `setups`, `story-get`, styled in `static/style.css`). Its illustrations are SVGs under
`docs/img/getting-started/`, each with its own light and dark palette, drawn by
`site/art/getting_started.py` (edit it and re-run it rather than editing the SVGs).

### Links

- Link to another page by its **source path, relative to the current file**, ending in `.md`:
  `[target versions](../guides/target-version.md#what-changes)`. The generator rewrites it to the
  pretty URL and **fails the build on a link to a page or policy that does not exist**.
- Link to a section landing page by its `_index.md`: `[policies](../policies/_index.md)`,
  `[guides](guides/_index.md)` (from `content/_index.md`).
- `#anchor` parts are checked too: the target page must have a heading with that auto-generated ID
  (lowercase, spaces to `-`, punctuation dropped: `## Default mode` → `#default-mode`). A bare
  `#anchor` links to a heading on the same page. The policy index also has one anchor per group
  (`#legacy`, `#mesh`, `#kong-mesh`, `#gateway`, `#resource`).
- Link to a policy page with the pseudo-path `policy:<Kind>`: `[MeshTimeout](policy:MeshTimeout)`.
- Link or embed a file that lives under the repository's `docs/` folder (the GitHub Pages root) with
  a **site-root path** starting with `/`: `![Pipeline](/img/pipeline.svg)`,
  `[overview](/assets/migration-overview.svg)`. The generator checks the file exists and rewrites
  the path relative to the page. Any other relative path that is not a `.md` page fails the build.
- Markdown inside `data/policies.yaml` (`notes`, `timeline[].change`) resolves relative links as if
  it were a file in `content/policies/`: `[meshes](../topics/meshes.md)`, `policy:<Kind>`.
- Link to source code with a full GitHub URL
  (`https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_mesh.go`).
- Link to upstream docs or UPGRADE notes with full public URLs only (github.com/kumahq/kuma,
  developer.konghq.com, kuma.io). Never a local filesystem path.

### Code blocks

Every fenced block needs a language tag; the build fails without one. Use:

| Tag | For |
|---|---|
| `yaml`, `json`, `go`, `markdown`, `diff` | files in that format |
| `bash` (or `shell`) | commands only, no output |
| `console` | commands prefixed `$ ` mixed with their output; the copy button copies the commands only |
| `text` | plain output, directory trees, usage synopses |

Highlighting happens at build time (`site/gen/highlight.go`): chroma tokenises the block and each
token becomes a `<span>` with chroma's short class name (`nt`, `s2`, `c1`, …), styled by the
site's own palette in `site/static/shared.css` (`--c-*` variables derived from the colour tokens,
each at least 4.5:1 against the code background in light and dark mode). Two extra passes:

- `bash`/`shell`/`console`: the first word of each command is classed `k-bin` (`kuma-migrator`),
  `k-manual` (`kubectl`/`kumactl`, the manual apply step) or `k-cmd` (anything else); then
  `k-sub` for the kuma-migrator subcommand, `k-flag` for flags, `k-val` for flag values.
- `text`: a leading `⚠`/`✗`/`✓`/`●`, `[WARN]`/`[INFO]`, and `← …`/`→ …` path annotations are
  coloured as the CLI prints them.

Each block is wrapped in `<div class="code">`; `copy.js` adds the copy button (none without
JavaScript).

### Callouts

A blockquote whose first line is `**Note:**`, `**Warning:**` or `**v3:**` renders as a coloured
callout.

## Policy data (`data/policies.yaml`)

```yaml
- kind: Timeout                 # exact resource kind; page slug is lowercase kind
  group: legacy                 # legacy | mesh | kong-mesh | gateway | resource
  product: kuma                 # kuma | kong-mesh
  summary: One line on what the resource does.
  v2:
    status: converted           # converted | rewritten | passthrough | warned | error | removed | skipped
    output: [MeshTimeout]       # kinds the migrator emits for this input (empty if none)
    notes: |                    # markdown; what the migrator does and why
      ...
  v3:
    status: converted
    output: [MeshTimeout]
    notes: |
      ...
  timeline:                     # upstream changes that matter for migration, oldest first
    - version: "2.6"
      change: Deprecated in favour of MeshTimeout.
    - version: "3.0"
      change: Removed.
  topics: [legacy-policies]     # topic slugs; rendered as links
  code: [pkg/migrator/legacyconf.go]   # repo-relative paths; rendered as GitHub links
```

Status meaning, per target:

| status | Meaning |
|---|---|
| `converted` | Rewritten into a different kind (`output`) |
| `rewritten` | Same kind, fields changed automatically |
| `passthrough` | Copied unchanged |
| `warned` | Copied, with warnings the operator must act on |
| `error` | Not converted; reported as manual work (under v3, written to `_manual/`) |
| `removed` | The kind no longer exists on that target; the report lists it for deletion |
| `skipped` | Not extracted or not processed by default |

## Section and slug map

Guides (`/docs/guides/`): `architecture`, `installation`, `getting-started` (alias `usage`), `upgrade-plans`, `cli`,
`extract`, `migrate`, `target-version`, `apply`, `output-and-reports`, `examples`, `notes`.

Topics (`/docs/topics/`): `meshes`, `mtls-identity`, `external-pki`, `meshservice`,
`multizone`, `gateways`, `external-services`, `access-control`, `observability`,
`policy-targeting`, `dataplanes`, `legacy-policies`.

## Writing style

- Plain, direct sentences. No metaphors or filler ("under the hood", "think of it as").
- No numbers in headings.
- Tables for comparisons, code blocks for anything runnable.
- Describe what the tool does now. Do not narrate earlier behaviour or abandoned approaches.
- Every claim about Kuma/Kong Mesh behaviour must be checkable: link the public UPGRADE note,
  docs page or source file it comes from.
