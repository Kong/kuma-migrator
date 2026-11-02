package main

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"gopkg.in/yaml.v3"
)

// Sections in navigation order.
var sections = []struct{ Key, Title string }{
	{"guides", "Guides"},
	{"topics", "Topics"},
	{"policies", "Policies"},
}

func sectionTitle(key string) string {
	for _, s := range sections {
		if s.Key == key {
			return s.Title
		}
	}
	return key
}

// FrontMatter is the YAML block at the top of every content page.
type FrontMatter struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Order       int    `yaml:"order"`
	NavTitle    string `yaml:"nav_title"`
}

// Heading is a rendered heading with its anchor.
type Heading struct {
	Level int
	ID    string
	Text  string
}

// Page is one Markdown source file.
type Page struct {
	Src      string // path relative to site/content, slash-separated: "guides/cli.md"
	File     string // path relative to the repo root, for error messages
	FM       FrontMatter
	Section  string // "" for the /docs/ landing page
	IsIndex  bool   // _index.md
	URL      string // output URL relative to docs/docs/: "guides/cli/", "" for the landing page
	Body     []byte
	BodyLine int // 1-based line number of the first body line in File

	doc      ast.Node
	headings []Heading
	ids      map[string]bool
	HTML     string
}

// NavTitle is the sidebar label.
func (p *Page) NavTitle() string {
	if p.FM.NavTitle != "" {
		return p.FM.NavTitle
	}
	return p.FM.Title
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// splitFrontMatter separates the YAML front matter from the Markdown body.
// It returns the YAML, the body and the 1-based line number the body starts on.
func splitFrontMatter(src []byte) (fm []byte, body []byte, bodyLine int, err error) {
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(src, []byte("---\n")) {
		return nil, nil, 0, fmt.Errorf("missing front matter: the file must start with a '---' line")
	}
	rest := src[4:]
	line := 2
	for off := 0; off <= len(rest); {
		end := bytes.IndexByte(rest[off:], '\n')
		var l []byte
		next := len(rest) + 1
		if end >= 0 {
			l = rest[off : off+end]
			next = off + end + 1
		} else {
			l = rest[off:]
		}
		if string(bytes.TrimRight(l, " \t")) == "---" {
			if next > len(rest) {
				return rest[:off], nil, line + 1, nil
			}
			return rest[:off], rest[next:], line + 1, nil
		}
		off = next
		line++
	}
	return nil, nil, 0, fmt.Errorf("unterminated front matter: no closing '---' line")
}

var yamlLineRe = regexp.MustCompile(`line (\d+)`)

// parseFrontMatter decodes front matter strictly (unknown keys are errors).
// Line numbers in YAML errors are shifted to file line numbers.
func parseFrontMatter(file string, src []byte) (FrontMatter, []byte, int, error) {
	var fm FrontMatter
	raw, body, bodyLine, err := splitFrontMatter(src)
	if err != nil {
		return fm, nil, 0, fmt.Errorf("%s:1: %v", file, err)
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&fm); err != nil {
			msg := yamlLineRe.ReplaceAllStringFunc(err.Error(), func(m string) string {
				n, _ := strconv.Atoi(m[5:])
				return "line " + strconv.Itoa(n+1)
			})
			return fm, nil, 0, fmt.Errorf("%s: front matter: %s", file, msg)
		}
	}
	if strings.TrimSpace(fm.Title) == "" {
		return fm, nil, 0, fmt.Errorf("%s:1: front matter: missing title", file)
	}
	return fm, body, bodyLine, nil
}

// loadPages reads site/content. Structural problems are appended to errs.
func (s *Site) loadPages() {
	contentDir := filepath.Join(s.root, "site", "content")
	err := filepath.WalkDir(contentDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(contentDir, p)
		rel = filepath.ToSlash(rel)
		file := "site/content/" + rel
		parts := strings.Split(rel, "/")
		pg := &Page{Src: rel, File: file}
		switch {
		case len(parts) == 1 && parts[0] == "_index.md":
			pg.IsIndex = true
		case len(parts) == 2 && isSection(parts[0]):
			pg.Section = parts[0]
			name := strings.TrimSuffix(parts[1], ".md")
			if name == "_index" {
				pg.IsIndex = true
				pg.URL = parts[0] + "/"
			} else if !slugRe.MatchString(name) {
				s.errorf("%s:1: file name %q is not a valid slug (lowercase letters, digits, '-')", file, name)
				return nil
			} else {
				pg.URL = parts[0] + "/" + name + "/"
			}
		default:
			s.errorf("%s:1: unexpected location: pages go in site/content/{guides,topics,policies}/<slug>.md", file)
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fm, body, bodyLine, err := parseFrontMatter(file, src)
		if err != nil {
			s.errs = append(s.errs, err.Error())
			return nil
		}
		pg.FM, pg.Body, pg.BodyLine = fm, body, bodyLine
		s.pages = append(s.pages, pg)
		return nil
	})
	if err != nil {
		s.errorf("site/content: %v", err)
		return
	}
	for _, pg := range s.pages {
		s.bySrc[pg.Src] = pg
		s.claimURL(pg.URL, pg.File)
	}
	if s.bySrc["_index.md"] == nil {
		s.errorf("site/content/_index.md: missing (landing page of /docs/)")
	}
	for _, sec := range sections {
		if s.bySrc[sec.Key+"/_index.md"] == nil {
			s.errorf("site/content/%s/_index.md: missing (section landing page)", sec.Key)
		}
	}
}

func isSection(k string) bool {
	for _, s := range sections {
		if s.Key == k {
			return true
		}
	}
	return false
}

// sectionPages returns the non-index pages of a section, sorted by order then title.
func (s *Site) sectionPages(sec string) []*Page {
	var out []*Page
	for _, p := range s.pages {
		if p.Section == sec && !p.IsIndex {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FM.Order != out[j].FM.Order {
			return out[i].FM.Order < out[j].FM.Order
		}
		if out[i].FM.Title != out[j].FM.Title {
			return out[i].FM.Title < out[j].FM.Title
		}
		return out[i].Src < out[j].Src
	})
	return out
}

// relURL returns the relative URL from the directory URL `from` to `to`
// (both relative to docs/docs/, directory-style with a trailing slash or "").
func relURL(from, to string) string {
	fs := splitURL(from)
	ts := splitURL(to)
	i := 0
	for i < len(fs) && i < len(ts) && fs[i] == ts[i] {
		i++
	}
	out := strings.Repeat("../", len(fs)-i) + strings.Join(ts[i:], "/")
	if len(ts) > i {
		out += "/"
	}
	if out == "" {
		return "./"
	}
	return out
}

func splitURL(u string) []string {
	u = strings.Trim(u, "/")
	if u == "" {
		return nil
	}
	return strings.Split(u, "/")
}

// rootPrefix is the relative path from a page URL to docs/docs/.
func rootPrefix(url string) string {
	return strings.Repeat("../", len(splitURL(url)))
}

func srcDir(src string) string {
	d := path.Dir(src)
	if d == "." {
		return ""
	}
	return d
}
