package main

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
)

func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(newCodeRenderer())),
	)
}

// mdDoc is a parsed Markdown fragment plus what is needed to report errors in it.
type mdDoc struct {
	file     string // repo-relative file for error messages
	line     int    // 1-based line in file where body starts
	body     []byte
	doc      ast.Node
	headings []Heading
	ids      map[string]bool
}

func (s *Site) parseMarkdown(file string, line int, body []byte) *mdDoc {
	d := &mdDoc{file: file, line: line, body: body, ids: map[string]bool{}}
	d.doc = s.md.Parser().Parse(text.NewReader(body), parser.WithContext(parser.NewContext()))
	_ = ast.Walk(d.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := n.(type) {
		case *ast.FencedCodeBlock:
			if msg := checkLang(string(c.Language(body))); msg != "" {
				s.errorf("%s:%d: %s", file, d.fenceLine(c), msg)
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			s.errorf("%s:%d: indented code block: use a fenced block with a language tag", file, d.fenceLine(c)+1)
			return ast.WalkSkipChildren, nil
		}
		if h, ok := n.(*ast.Heading); ok {
			id, _ := h.AttributeString("id")
			ids, _ := id.([]byte)
			hd := Heading{Level: h.Level, ID: string(ids), Text: plainText(h, body)}
			d.headings = append(d.headings, hd)
			d.ids[hd.ID] = true
		}
		return ast.WalkContinue, nil
	})
	return d
}

// plainText returns the text content of an inline subtree.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// fenceLine returns the file line of a code block's opening fence.
func (d *mdDoc) fenceLine(n ast.Node) int {
	if n.Lines().Len() > 0 {
		return d.line + bytes.Count(d.body[:n.Lines().At(0).Start], []byte("\n")) - 1
	}
	if f, ok := n.(*ast.FencedCodeBlock); ok && f.Info != nil {
		return d.line + bytes.Count(d.body[:f.Info.Segment.Start], []byte("\n"))
	}
	return d.lineOf(n)
}

// lineOf returns the file line number of a node, best effort.
func (d *mdDoc) lineOf(n ast.Node) int {
	off := -1
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			off = t.Segment.Start
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	for p := n; off < 0 && p != nil; p = p.Parent() {
		if p.Type() == ast.TypeBlock && p.Lines().Len() > 0 {
			off = p.Lines().At(0).Start
		}
	}
	if off < 0 {
		return d.line
	}
	return d.line + bytes.Count(d.body[:off], []byte("\n"))
}

// linkCtx says where a Markdown fragment lives, for resolving its links.
type linkCtx struct {
	srcDir string // directory of the source, relative to site/content ("" for the root)
	url    string // output URL of the page that renders it
}

// rewriteLinks resolves every link and image destination in d, recording errors.
func (s *Site) rewriteLinks(d *mdDoc, ctx linkCtx) {
	_ = ast.Walk(d.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest *[]byte
		switch l := n.(type) {
		case *ast.Link:
			dest = &l.Destination
		case *ast.Image:
			dest = &l.Destination
		case *ast.AutoLink:
			return ast.WalkContinue, nil
		default:
			return ast.WalkContinue, nil
		}
		out, err := s.resolveLink(string(*dest), ctx, d.ids)
		if err != nil {
			s.errorf("%s:%d: %v", d.file, d.lineOf(n), err)
			return ast.WalkContinue, nil
		}
		*dest = []byte(out)
		return ast.WalkContinue, nil
	})
}

func hasScheme(u string) bool {
	i := strings.Index(u, ":")
	if i <= 0 {
		return false
	}
	for _, c := range u[:i] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// resolveLink maps a link destination as written in Markdown to the URL written to HTML.
//
//	policy:<Kind>[#anchor]   policy page
//	#anchor                  heading on the same page
//	/path/to/file            file under docs/ (the GitHub Pages root), e.g. /img/pipeline.svg
//	rel/path.md[#anchor]     another content page, relative to the source file
//	scheme://...             external, unchanged
func (s *Site) resolveLink(dest string, ctx linkCtx, selfIDs map[string]bool) (string, error) {
	if dest == "" {
		return "", fmt.Errorf("empty link destination")
	}
	target, anchor, _ := strings.Cut(dest, "#")
	if strings.HasPrefix(dest, "policy:") {
		kind := strings.TrimPrefix(target, "policy:")
		p := s.policyByKind[kind]
		if p == nil {
			return "", fmt.Errorf("dangling link %q: no policy with kind %q in site/data/policies.yaml", dest, kind)
		}
		if anchor != "" && !policyPageIDs[anchor] {
			return "", fmt.Errorf("dangling link %q: policy pages have no anchor #%s", dest, anchor)
		}
		return withAnchor(relURL(ctx.url, p.URL), anchor), nil
	}
	if hasScheme(dest) {
		return dest, nil
	}
	if target == "" {
		if selfIDs != nil && !selfIDs[anchor] {
			return "", fmt.Errorf("dangling link %q: no heading with that anchor on this page", dest)
		}
		return dest, nil
	}
	if strings.HasPrefix(target, "/") {
		clean := path.Clean(target)
		q := ""
		if i := strings.IndexByte(clean, '?'); i >= 0 {
			clean, q = clean[:i], clean[i:]
		}
		if clean == "/docs" || strings.HasPrefix(clean, "/docs/") {
			return "", fmt.Errorf("link %q points into the generated docs/docs/ tree: link the source .md file instead", dest)
		}
		fsPath := filepath.Join(s.root, "docs", filepath.FromSlash(strings.TrimPrefix(clean, "/")))
		if _, err := os.Stat(fsPath); err != nil {
			return "", fmt.Errorf("dangling link %q: docs%s does not exist", dest, clean)
		}
		out := rootPrefix(ctx.url) + "../" + strings.TrimPrefix(clean, "/")
		if strings.HasSuffix(target, "/") {
			out += "/"
		}
		return withAnchor(out+q, anchor), nil
	}
	if !strings.HasSuffix(target, ".md") {
		return "", fmt.Errorf("unsupported relative link %q: link pages by their .md source path, repo files under docs/ by site-root path (/img/x.svg), anything else by full URL", dest)
	}
	src := path.Clean(path.Join(ctx.srcDir, target))
	pg := s.bySrc[src]
	if pg == nil {
		return "", fmt.Errorf("dangling link %q: site/content/%s does not exist", dest, src)
	}
	if anchor != "" {
		ids := pg.ids
		if pg.Src == "policies/_index.md" {
			ids = mergeIDs(ids, policyIndexIDs)
		}
		if !ids[anchor] {
			return "", fmt.Errorf("dangling link %q: site/content/%s has no heading with anchor #%s", dest, src, anchor)
		}
	}
	return withAnchor(relURL(ctx.url, pg.URL), anchor), nil
}

func withAnchor(u, anchor string) string {
	if anchor == "" {
		return u
	}
	return u + "#" + anchor
}

func mergeIDs(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

var calloutKinds = map[string]string{"Note:": "note", "Warning:": "warning", "v3:": "v3"}

// markCallouts adds a class to blockquotes that start with **Note:**, **Warning:** or **v3:**.
func markCallouts(d *mdDoc) {
	_ = ast.Walk(d.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		bq, ok := n.(*ast.Blockquote)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		para, ok := bq.FirstChild().(*ast.Paragraph)
		if !ok {
			return ast.WalkContinue, nil
		}
		em, ok := para.FirstChild().(*ast.Emphasis)
		if !ok || em.Level != 2 {
			return ast.WalkContinue, nil
		}
		if kind, ok := calloutKinds[plainText(em, d.body)]; ok {
			bq.SetAttributeString("class", []byte("callout callout-"+kind))
		}
		return ast.WalkContinue, nil
	})
}

// render renders a parsed document to HTML. Tables are wrapped in a scroll container.
func (s *Site) render(d *mdDoc) string {
	markCallouts(d)
	var buf bytes.Buffer
	if err := s.md.Renderer().Render(&buf, d.body, d.doc); err != nil {
		s.errorf("%s: render: %v", d.file, err)
	}
	out := buf.String()
	out = strings.ReplaceAll(out, "<table>", `<div class="table-wrap"><table>`)
	out = strings.ReplaceAll(out, "</table>", "</table></div>")
	return out
}

// renderInline renders a one-paragraph fragment without the surrounding <p>.
func (s *Site) renderInline(d *mdDoc) string {
	out := strings.TrimSpace(s.render(d))
	if strings.HasPrefix(out, "<p>") && strings.HasSuffix(out, "</p>") && strings.Count(out, "<p>") == 1 {
		out = out[3 : len(out)-4]
	}
	return out
}
