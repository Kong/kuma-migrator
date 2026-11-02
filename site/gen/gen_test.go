package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture copies testdata/fixture plus the real templates and static files into a temp root.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyDir(t, "testdata/fixture", root)
	copyDir(t, "../templates", filepath.Join(root, "site", "templates"))
	copyDir(t, "../static", filepath.Join(root, "site", "static"))
	return root
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func build(t *testing.T, root string) (int, error) {
	t.Helper()
	return Build(Config{Root: root, LastMod: "2026-01-02", Release: "v1.2.3"})
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustContain(t *testing.T, what, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%s: missing %q", what, sub)
		}
	}
}

func TestFrontMatter(t *testing.T) {
	fm, body, line, err := parseFrontMatter("x.md", []byte("---\ntitle: Hello\ndescription: D\norder: 3\nnav_title: H\n---\n\n## A\n"))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Title != "Hello" || fm.Description != "D" || fm.Order != 3 || fm.NavTitle != "H" {
		t.Errorf("unexpected front matter %+v", fm)
	}
	if line != 7 || string(body) != "\n## A\n" {
		t.Errorf("body line %d body %q", line, body)
	}

	cases := map[string]string{
		"no front matter": "## A\n",
		"missing title":   "---\ndescription: x\n---\nbody",
		"unknown key":     "---\ntitle: x\ntitel: y\n---\nbody",
		"unterminated":    "---\ntitle: x\n",
	}
	for name, src := range cases {
		if _, _, _, err := parseFrontMatter("x.md", []byte(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	_, _, _, err = parseFrontMatter("x.md", []byte("---\ntitle: x\ntitel: y\n---\n"))
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("unknown key error should carry the file line: %v", err)
	}
}

func TestRelURL(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"guides/cli/", "topics/meshes/", "../../topics/meshes/"},
		{"guides/cli/", "guides/extract/", "../extract/"},
		{"guides/cli/", "guides/", "../"},
		{"guides/", "guides/cli/", "cli/"},
		{"", "guides/cli/", "guides/cli/"},
		{"guides/cli/", "", "../../"},
		{"guides/cli/", "guides/cli/", "./"},
	}
	for _, c := range cases {
		if got := relURL(c.from, c.to); got != c.want {
			t.Errorf("relURL(%q, %q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestBuildFixture(t *testing.T) {
	root := fixture(t)
	n, err := build(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 9 { // home, 3 section indexes, 3 pages, 2 policies
		t.Errorf("wrote %d pages, want 9", n)
	}
	cli := read(t, root, "docs/docs/guides/cli/index.html")
	mustContain(t, "cli page", cli,
		`<title>CLI reference – kuma-migrator docs</title>`,
		`<link rel="canonical" href="https://kong.github.io/kuma-migrator/docs/guides/cli/">`,
		`<meta name="description" content="Every command and flag.">`,
		`href="../../topics/meshes/#default-mode"`,
		`href="../../policies/meshtimeout/"`,
		`src="../../../img/pipeline.svg"`,
		`href="#commands"`,
		`<h2 id="commands">Commands</h2>`,
		`<div class="code"><pre class="chroma" data-lang="go"><code>`,
		`<span class="kd">func</span>`,
		`<div class="table-wrap"><table>`,
		`<blockquote class="callout callout-note">`,
		`<blockquote class="callout callout-v3">`,
		`href="../../static/style.css"`,
		`<a class="brand" href="../../../">`,
		`class="toc-l3"><a href="#sub-heading">`,
		`<span>Next</span>Extracting`,
		`>CLI</a>`, // nav_title in sidebar
		`<!-- release-tag --><a class="release-badge" href="https://github.com/Kong/kuma-migrator/releases/latest" title="Latest release: v1.2.3">v1.2.3</a><!-- /release-tag -->`,
		`href="../../static/shared.css"`,
		`src="../../static/copy.js"`,
	)
	if strings.Contains(cli, `style="`) {
		t.Error("highlighted code must use classes, not inline styles")
	}

	home := read(t, root, "docs/docs/index.html")
	mustContain(t, "home", home, `href="guides/cli/"`, `href="policies/"`, `<a class="brand" href="../">`, `class="card" href="topics/"`)

	idx := read(t, root, "docs/docs/policies/index.html")
	mustContain(t, "policy index", idx,
		`<h2 id="legacy">Legacy policies</h2>`,
		`<h2 id="mesh">Mesh* policies</h2>`,
		`<a href="timeout/">Timeout</a>`,
		`<span class="status status-converted">converted</span> <a href="meshtimeout/"><code>MeshTimeout</code></a>`,
		`<span class="status status-removed">removed</span>`,
	)

	pol := read(t, root, "docs/docs/policies/timeout/index.html")
	mustContain(t, "policy page", pol,
		`<title>Timeout – kuma-migrator docs</title>`,
		`<span class="status status-converted">converted</span>`,
		`<a href="../meshtimeout/"><code>MeshTimeout</code></a>`,
		`See <a href="../../topics/meshes/">meshes</a>`,
		`<td class="version">2.6</td><td>Deprecated in favour of <a href="../meshtimeout/">MeshTimeout</a>.</td>`,
		`<a href="../../topics/meshes/">Meshes</a>`,
		`href="https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/legacyconf.go"`,
		`<h2 id="source-files">`,
	)

	sm := read(t, root, "docs/sitemap.xml")
	mustContain(t, "sitemap", sm,
		"<loc>https://kong.github.io/kuma-migrator/</loc>",
		"<loc>https://kong.github.io/kuma-migrator/docs/policies/timeout/</loc>",
		"<lastmod>2026-01-02</lastmod>",
	)
	for _, f := range []string{"static/shared.css", "static/copy.js", "static/search-index.js", "static/search.js", "static/style.css", "static/favicon.svg"} {
		read(t, root, "docs/docs/"+f)
	}
	css := read(t, root, "docs/docs/static/shared.css")
	mustContain(t, "shared css", css, "@media (prefers-color-scheme: dark)", ".chroma .kd", ".chroma .k-bin", "--c-key")
}

func TestHighlight(t *testing.T) {
	cases := []struct {
		lang, code string
		want       []string
	}{
		{"yaml", "kind: MeshTimeout # c\nspec:\n  port: 8080\n  on: true\n  s: \"x\"\n", []string{
			`<span class="nt">kind</span><span class="p">:</span> <span class="l">MeshTimeout</span> <span class="c"># c</span>`,
			`<span class="m">8080</span>`, `<span class="kc">true</span>`, `<span class="s2">"x"</span>`,
		}},
		{"bash", "# extract\nkuma-migrator migrate --input-dir ./in \\\n  --dry-run --to-latest=v3\nkubectl --context prod apply -f ./out/\nless ./out/plan.md\n", []string{
			`<span class="c1"># extract</span>`,
			`<span class="k-bin">kuma-migrator</span> <span class="k-sub">migrate</span> <span class="k-flag">--input-dir</span> <span class="k-val">./in</span>`,
			`<span class="k-flag">--dry-run</span> <span class="k-flag">--to-latest</span><span class="o">=</span><span class="k-val">v3</span>`,
			`<span class="k-manual">kubectl</span> <span class="k-flag">--context</span> <span class="k-val">prod</span> <span class="k-manual-b">apply</span> <span class="k-flag">-f</span> <span class="k-val">./out/</span>`,
			`<span class="k-cmd">less</span> ./out/plan.md`,
		}},
		{"console", "$ kuma-migrator version\nkuma-migrator v0.8.0\n", []string{
			`<span class="gp">$</span> <span class="k-bin">kuma-migrator</span> <span class="k-sub">version</span>`,
		}},
		{"text", "  ✓  LEGACY  a.yaml\n       ←  in/a.yaml\n  ⚠  careful\na → b\ndir/   ← what\n", []string{
			`  <span class="o-ok">✓</span>  LEGACY  a.yaml`,
			`       <span class="o-dim">←  in/a.yaml</span>`,
			`<span class="o-warn">⚠</span>  careful`,
			"\na → b\n",
			`dir/   <span class="o-dim">← what</span>`,
		}},
	}
	for _, c := range cases {
		got := highlight(c.lang, c.code)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", c.lang, w, got)
			}
		}
	}
}

func TestLandingSync(t *testing.T) {
	root := fixture(t)
	writeFile(t, root, "docs/index.html", "<html><head><style>\n/* shared.css: generated */\nold css\n/* /shared.css */\n</style></head><body>\n"+
		"<!-- release-tag -->v0.0.1<!-- /release-tag -->\n<a href=\"docs/\">Docs</a>\n"+
		"<script>\n/* copy.js: generated */\n/* /copy.js */\n</script></body></html>\n")
	if _, err := build(t, root); err != nil {
		t.Fatal(err)
	}
	got := read(t, root, "docs/index.html")
	mustContain(t, "landing", got,
		`<!-- release-tag --><a class="release-badge" href="https://github.com/Kong/kuma-migrator/releases/latest" title="Latest release: v1.2.3">v1.2.3</a><!-- /release-tag -->`,
		"/* shared.css: generated */\n/* Shared by the /docs/ pages",
		".chroma .k-bin",
		"/* copy.js: generated */\n// Copy-to-clipboard",
	)
	if strings.Contains(got, "old css") {
		t.Error("shared.css region not replaced")
	}
	if _, err := build(t, root); err != nil {
		t.Fatal(err)
	}
	if again := read(t, root, "docs/index.html"); again != got {
		t.Error("landing sync is not idempotent")
	}
	if _, err := Build(Config{Root: root, LastMod: "2026-01-02", Release: "none"}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, "landing without release", read(t, root, "docs/index.html"), "<!-- release-tag --><!-- /release-tag -->\n")
}

func TestStaleFilesRemoved(t *testing.T) {
	root := fixture(t)
	writeFile(t, root, "docs/docs/old/index.html", "stale")
	if _, err := build(t, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/docs/old")); !os.IsNotExist(err) {
		t.Error("stale output not removed")
	}
}

func TestDeterministic(t *testing.T) {
	root := fixture(t)
	snap := func() map[string][]byte {
		if _, err := build(t, root); err != nil {
			t.Fatal(err)
		}
		m := map[string][]byte{}
		_ = filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(p)
				m[p] = b
			}
			return nil
		})
		return m
	}
	a, b := snap(), snap()
	if len(a) != len(b) {
		t.Fatalf("file count differs: %d vs %d", len(a), len(b))
	}
	for k, v := range a {
		if !bytes.Equal(v, b[k]) {
			t.Errorf("%s differs between builds", k)
		}
	}
}

// buildErr builds the fixture after applying edits and returns the error text.
func buildErr(t *testing.T, edits map[string]string) string {
	t.Helper()
	root := fixture(t)
	for rel, content := range edits {
		writeFile(t, root, rel, content)
	}
	_, err := build(t, root)
	if err == nil {
		t.Fatal("expected the build to fail")
	}
	return err.Error()
}

func TestBuildFailures(t *testing.T) {
	page := func(body string) string { return "---\ntitle: P\n---\n\n## H\n\n" + body + "\n" }
	cases := []struct {
		name  string
		edits map[string]string
		want  string
	}{
		{"dangling md link", map[string]string{"site/content/guides/p.md": page("[x](../topics/nope.md)")},
			`site/content/guides/p.md:7: dangling link "../topics/nope.md": site/content/topics/nope.md does not exist`},
		{"dangling anchor", map[string]string{"site/content/guides/p.md": page("[x](cli.md#nope)")},
			`site/content/guides/p.md:7: dangling link "cli.md#nope"`},
		{"dangling self anchor", map[string]string{"site/content/guides/p.md": page("[x](#nope)")},
			`dangling link "#nope"`},
		{"dangling policy", map[string]string{"site/content/guides/p.md": page("[x](policy:Nope)")},
			`dangling link "policy:Nope"`},
		{"missing static file", map[string]string{"site/content/guides/p.md": page("![x](/img/nope.svg)")},
			`dangling link "/img/nope.svg": docs/img/nope.svg does not exist`},
		{"relative non-page link", map[string]string{"site/content/guides/p.md": page("![x](../../../img/pipeline.svg)")},
			`unsupported relative link`},
		{"missing title", map[string]string{"site/content/guides/p.md": "---\ndescription: d\n---\nbody\n"},
			`site/content/guides/p.md:1: front matter: missing title`},
		{"duplicate slug", map[string]string{"site/content/policies/timeout.md": page("")},
			`duplicate slug: /docs/policies/timeout/`},
		{"unknown status", map[string]string{"site/data/policies.yaml": "- kind: X\n  group: legacy\n  product: kuma\n  summary: s\n  v2: {status: bogus}\n  v3: {status: removed}\n"},
			`site/data/policies.yaml:1 (X): unknown v2.status "bogus"`},
		{"unknown group", map[string]string{"site/data/policies.yaml": "- kind: X\n  group: misc\n  product: kuma\n  summary: s\n  v2: {status: error}\n  v3: {status: removed}\n"},
			`unknown group "misc"`},
		{"unknown topic", map[string]string{"site/data/policies.yaml": "- kind: X\n  group: legacy\n  product: kuma\n  summary: s\n  v2: {status: error}\n  v3: {status: removed}\n  topics: [nope]\n"},
			`site/data/policies.yaml:7 (X): topic "nope" has no page site/content/topics/nope.md`},
		{"dangling link in notes", map[string]string{"site/data/policies.yaml": "- kind: X\n  group: legacy\n  product: kuma\n  summary: s\n  v2:\n    status: error\n    notes: |\n      line one\n      [x](policy:Nope)\n  v3: {status: removed}\n"},
			`site/data/policies.yaml:9: dangling link "policy:Nope"`},
		{"landing link", map[string]string{"docs/index.html": `<a href="docs/guides/nope/">x</a>`},
			`docs/index.html:1: link "docs/guides/nope/" does not match a generated page`},
		{"untagged fence", map[string]string{"site/content/guides/p.md": page("text\n\n```\nx\n```")},
			"site/content/guides/p.md:9: code block without a language tag"},
		{"unknown fence language", map[string]string{"site/content/guides/p.md": page("```nosuchlang\nx\n```")},
			`site/content/guides/p.md:7: code block language "nosuchlang" is not known to chroma`},
		{"indented code block", map[string]string{"site/content/guides/p.md": page("para\n\n    indented\n")},
			"site/content/guides/p.md:9: indented code block"},
		{"untagged fence in notes", map[string]string{"site/data/policies.yaml": "- kind: X\n  group: legacy\n  product: kuma\n  summary: s\n  v2:\n    status: error\n    notes: |\n      line one\n\n      ```\n      x\n      ```\n  v3: {status: removed}\n"},
			"site/data/policies.yaml:10: code block without a language tag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildErr(t, c.edits)
			if !strings.Contains(got, c.want) {
				t.Errorf("error output\n%s\ndoes not contain\n%s", got, c.want)
			}
		})
	}
}
