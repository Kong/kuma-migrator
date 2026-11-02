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
that does not exist, or a `docs/...` link on `docs/index.html` that matches no generated page.

The sitemap `<lastmod>` is the date of the last commit touching `site/` (today's date while `site/`
has uncommitted changes); `make site SITE_LASTMOD=YYYY-MM-DD` pins it. `site-check` ignores
`<lastmod>` lines.

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
  static/                   CSS/JS/SVG copied to docs/docs/static/ (generator-owned)
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
---
```

Do not repeat the title as a `# heading` in the body; start at `##`. Each `##` becomes an entry in
the on-page table of contents.

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

Guides (`/docs/guides/`): `architecture`, `installation`, `usage`, `upgrade-plans`, `cli`,
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
