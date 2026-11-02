package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
)

const (
	siteURL  = "https://kong.github.io/kuma-migrator/"
	docsURL  = siteURL + "docs/"
	repoBlob = "https://github.com/Kong/kuma-migrator/blob/main/"
	repoURL  = "https://github.com/Kong/kuma-migrator"
)

// Config controls a build.
type Config struct {
	Root    string // repository root
	LastMod string // sitemap lastmod; empty = from git
	OutDir  string // output directory; empty = <root>/docs/docs
	Sitemap string // sitemap path; empty = <root>/docs/sitemap.xml
	// Release is the tag shown in the header badge. Empty = `git describe --tags --abbrev=0`;
	// "none" = no badge.
	Release string
}

// Site holds everything loaded for one build.
type Site struct {
	root         string
	md           goldmark.Markdown
	pages        []*Page
	bySrc        map[string]*Page
	policies     []*Policy
	policyByKind map[string]*Policy
	urls         map[string]string // output URL -> who claimed it
	errs         []string
	tmpl         map[string]*template.Template
	release      template.HTML // header badge line, markers included
}

func (s *Site) errorf(format string, args ...any) {
	s.errs = append(s.errs, fmt.Sprintf(format, args...))
}

func (s *Site) claimURL(url, by string) {
	if prev, ok := s.urls[url]; ok {
		s.errorf("%s: duplicate slug: /docs/%s is already produced by %s", by, url, prev)
		return
	}
	s.urls[url] = by
}

// BuildError lists every problem found.
type BuildError []string

func (e BuildError) Error() string {
	return fmt.Sprintf("site build failed with %d error(s):\n  %s", len(e), strings.Join(e, "\n  "))
}

// Build generates the site. It returns the number of HTML pages written.
func Build(cfg Config) (int, error) {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return 0, err
	}
	s := &Site{
		root:         root,
		md:           newMarkdown(),
		bySrc:        map[string]*Page{},
		policyByKind: map[string]*Policy{},
		urls:         map[string]string{},
	}
	rel := cfg.Release
	if rel == "" {
		rel = gitRelease(root)
	}
	if rel == "none" {
		rel = ""
	}
	s.release = template.HTML(releaseBadge(rel))
	s.loadPolicies()
	s.loadPages()
	s.checkPolicyTopics()
	if err := s.loadTemplates(); err != nil {
		s.errs = append(s.errs, err.Error())
		return 0, BuildError(s.errs)
	}
	// Structural errors so far do not stop the build: links are still checked
	// so one run reports everything.

	// Parse every page first so heading anchors are known before links are resolved.
	docs := map[*Page]*mdDoc{}
	for _, p := range s.pages {
		d := s.parseMarkdown(p.File, p.BodyLine, p.Body)
		p.doc, p.headings, p.ids = d.doc, d.headings, d.ids
		docs[p] = d
	}
	for _, p := range s.pages {
		d := docs[p]
		s.rewriteLinks(d, linkCtx{srcDir: srcDir(p.Src), url: p.URL})
		p.HTML = s.render(d)
	}
	out := map[string][]byte{} // path relative to out dir -> content
	s.renderPolicies(out)
	s.renderPages(out)
	s.checkLanding()
	if len(s.errs) > 0 {
		return 0, BuildError(s.errs)
	}

	if err := s.addStatic(out); err != nil {
		return 0, err
	}
	lastmod := cfg.LastMod
	if lastmod == "" {
		lastmod = gitLastMod(root)
	}
	sitemap := s.sitemap(lastmod)

	outDir := cfg.OutDir
	if outDir == "" {
		outDir = filepath.Join(root, "docs", "docs")
	}
	smPath := cfg.Sitemap
	if smPath == "" {
		smPath = filepath.Join(root, "docs", "sitemap.xml")
	}
	if err := writeTree(outDir, out); err != nil {
		return 0, err
	}
	if err := os.WriteFile(smPath, sitemap, 0o644); err != nil {
		return 0, err
	}
	if err := s.syncLanding(); err != nil {
		return 0, err
	}
	n := 0
	for k := range out {
		if strings.HasSuffix(k, ".html") {
			n++
		}
	}
	return n, nil
}

// writeTree replaces dir with exactly the given files.
func writeTree(dir string, files map[string][]byte) error {
	if filepath.Base(dir) == "" || dir == "/" {
		return fmt.Errorf("refusing to clear %q", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := filepath.Join(dir, filepath.FromSlash(k))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, files[k], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// gitLastMod picks the sitemap date: today when site/ has uncommitted changes
// (the commit about to be made), otherwise the date of the last commit touching site/.
func gitLastMod(root string) string {
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		b, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	if run("status", "--porcelain", "--", "site") != "" {
		return time.Now().UTC().Format("2006-01-02")
	}
	if d := run("log", "-1", "--format=%cs", "--", "site"); d != "" {
		return d
	}
	if d := run("log", "-1", "--format=%cs"); d != "" {
		return d
	}
	return "1970-01-01"
}

// ---- templates and views ----

func (s *Site) loadTemplates() error {
	dir := filepath.Join(s.root, "site", "templates")
	base := filepath.Join(dir, "base.html")
	s.tmpl = map[string]*template.Template{}
	for _, name := range []string{"home", "section", "page", "policy-index", "policy"} {
		t, err := template.New("base.html").ParseFiles(base, filepath.Join(dir, name+".html"))
		if err != nil {
			return fmt.Errorf("site/templates: %v", err)
		}
		s.tmpl[name] = t
	}
	return nil
}

// Link is a rendered hyperlink.
type Link struct {
	Title       string
	URL         string
	Description string
	Active      bool
}

// NavGroup is a titled list of sidebar links.
type NavGroup struct {
	Title string
	Links []Link
}

// View is the data passed to templates.
type View struct {
	Title       string
	PageTitle   string
	Description string
	Canonical   string
	OGType      string
	Root        string // relative path to docs/docs/ ("" on the docs landing page)
	Home        string // href of /docs/
	Landing     string // href of the site landing page
	Section     string
	SectionName string
	SectionURL  string
	TopNav      []Link
	Nav         []NavGroup
	TOC         []Heading
	Prev, Next  *Link
	Content     template.HTML
	Cards       []Link
	Groups      []PolicyGroupView
	Policy      *PolicyView
	RepoURL     string
	EditURL     string
	Release     template.HTML
}

func (s *Site) newView(url, title, desc, section string) *View {
	root := rootPrefix(url)
	home := root
	if home == "" {
		home = "./"
	}
	v := &View{
		Title:       title,
		PageTitle:   title + " – kuma-migrator docs",
		Description: desc,
		Canonical:   docsURL + url,
		OGType:      "article",
		Root:        root,
		Home:        home,
		Landing:     root + "../",
		Section:     section,
		SectionName: sectionTitle(section),
		SectionURL:  relURL(url, section+"/"),
		RepoURL:     repoURL,
		Release:     s.release,
	}
	for _, sec := range sections {
		v.TopNav = append(v.TopNav, Link{Title: sec.Title, URL: relURL(url, sec.Key+"/"), Active: sec.Key == section})
	}
	return v
}

func (s *Site) exec(out map[string][]byte, name, url string, v *View) {
	var buf bytes.Buffer
	if err := s.tmpl[name].Execute(&buf, v); err != nil {
		s.errorf("site/templates/%s.html: rendering /docs/%s: %v", name, url, err)
		return
	}
	out[url+"index.html"] = buf.Bytes()
}

func toc(hs []Heading) []Heading {
	var out []Heading
	for _, h := range hs {
		if h.Level == 2 || h.Level == 3 {
			out = append(out, h)
		}
	}
	return out
}

func (s *Site) sectionNav(sec, current string) []NavGroup {
	idx := s.bySrc[sec+"/_index.md"]
	overview := Link{Title: "Overview", URL: relURL(current, sec+"/"), Active: current == sec+"/"}
	if idx != nil && idx.FM.NavTitle != "" {
		overview.Title = idx.FM.NavTitle
	}
	groups := []NavGroup{{Links: []Link{overview}}}
	if sec == "policies" {
		for _, g := range policyGroups {
			ng := NavGroup{Title: g.Title}
			for _, p := range s.policiesInGroup(g.Key) {
				ng.Links = append(ng.Links, Link{Title: p.Kind, URL: relURL(current, p.URL), Active: current == p.URL})
			}
			if len(ng.Links) > 0 {
				groups = append(groups, ng)
			}
		}
	}
	for _, p := range s.sectionPages(sec) {
		groups[0].Links = append(groups[0].Links, Link{Title: p.NavTitle(), URL: relURL(current, p.URL), Active: current == p.URL})
	}
	return groups
}

// prevNext returns neighbours of current in the flattened section order (excluding the overview).
func prevNext(nav []NavGroup, current string) (*Link, *Link) {
	var flat []Link
	for gi, g := range nav {
		for li, l := range g.Links {
			if gi == 0 && li == 0 {
				continue
			}
			flat = append(flat, l)
		}
	}
	for i, l := range flat {
		if l.Active {
			var p, n *Link
			if i > 0 {
				p = &flat[i-1]
			}
			if i+1 < len(flat) {
				n = &flat[i+1]
			}
			return p, n
		}
	}
	return nil, nil
}

func (s *Site) renderPages(out map[string][]byte) {
	for _, p := range s.pages {
		v := s.newView(p.URL, p.FM.Title, p.FM.Description, p.Section)
		v.Content = template.HTML(p.HTML)
		v.EditURL = repoBlob + p.File
		switch {
		case p.Section == "":
			v.OGType = "website"
			for _, sec := range sections {
				if idx := s.bySrc[sec.Key+"/_index.md"]; idx != nil {
					v.Cards = append(v.Cards, Link{Title: idx.FM.Title, URL: relURL(p.URL, idx.URL), Description: idx.FM.Description})
				}
			}
			s.exec(out, "home", p.URL, v)
		case p.IsIndex && p.Section == "policies":
			v.Nav = s.sectionNav(p.Section, p.URL)
			v.Groups = s.policyGroupViews(p.URL)
			v.TOC = toc(p.headings)
			for _, g := range v.Groups {
				v.TOC = append(v.TOC, Heading{Level: 2, ID: g.ID, Text: g.Title})
			}
			s.exec(out, "policy-index", p.URL, v)
		case p.IsIndex:
			v.Nav = s.sectionNav(p.Section, p.URL)
			v.TOC = toc(p.headings)
			for _, c := range s.sectionPages(p.Section) {
				v.Cards = append(v.Cards, Link{Title: c.FM.Title, URL: relURL(p.URL, c.URL), Description: c.FM.Description})
			}
			s.exec(out, "section", p.URL, v)
		default:
			v.Nav = s.sectionNav(p.Section, p.URL)
			v.TOC = toc(p.headings)
			v.Prev, v.Next = prevNext(v.Nav, p.URL)
			s.exec(out, "page", p.URL, v)
		}
	}
}

// ---- policies ----

// TargetView is one side of the v2/v3 comparison.
type TargetView struct {
	Name    string
	Label   string
	Status  string
	Outputs []Link
	Notes   template.HTML
}

// PolicyView is the data for a policy page or index row.
type PolicyView struct {
	Kind     string
	URL      string
	Summary  string
	Group    string
	Product  string
	V2, V3   TargetView
	Timeline []TimelineView
	Topics   []Link
	Code     []Link
}

// TimelineView is one rendered timeline row.
type TimelineView struct {
	Version string
	Change  template.HTML
}

// PolicyGroupView is one table on the policy index.
type PolicyGroupView struct {
	ID       string
	Title    string
	Policies []*PolicyView
}

func (s *Site) policyView(p *Policy, from string, full bool) *PolicyView {
	pv := &PolicyView{
		Kind:    p.Kind,
		URL:     relURL(from, p.URL),
		Summary: p.Summary,
		Group:   groupTitle(p.Group),
		Product: validProducts[p.Product],
	}
	ctx := linkCtx{srcDir: "policies", url: from}
	target := func(name, label string, t Target) TargetView {
		tv := TargetView{Name: name, Label: label, Status: t.Status}
		for _, k := range t.Output {
			l := Link{Title: k}
			if op := s.policyByKind[k]; op != nil {
				l.URL = relURL(from, op.URL)
			}
			tv.Outputs = append(tv.Outputs, l)
		}
		if full && strings.TrimSpace(t.Notes) != "" {
			d := s.parseMarkdown(policiesFile, p.valueLine(name, "notes"), []byte(t.Notes))
			s.rewriteLinks(d, ctx)
			tv.Notes = template.HTML(s.render(d))
		}
		return tv
	}
	pv.V2 = target("v2", "--to-latest v2 (2.x)", p.V2)
	pv.V3 = target("v3", "--to-latest v3 (3.0)", p.V3)
	if !full {
		return pv
	}
	for _, e := range p.Timeline {
		d := s.parseMarkdown(policiesFile, p.valueLine("timeline"), []byte(e.Change))
		s.rewriteLinks(d, ctx)
		pv.Timeline = append(pv.Timeline, TimelineView{Version: e.Version, Change: template.HTML(s.renderInline(d))})
	}
	for _, t := range p.Topics {
		if tp := s.bySrc["topics/"+t+".md"]; tp != nil {
			pv.Topics = append(pv.Topics, Link{Title: tp.FM.Title, URL: relURL(from, tp.URL), Description: tp.FM.Description})
		}
	}
	for _, c := range p.Code {
		pv.Code = append(pv.Code, Link{Title: c, URL: repoBlob + c})
	}
	return pv
}

func (s *Site) policyGroupViews(from string) []PolicyGroupView {
	var out []PolicyGroupView
	for _, g := range policyGroups {
		gv := PolicyGroupView{ID: g.Key, Title: g.Title}
		for _, p := range s.policiesInGroup(g.Key) {
			gv.Policies = append(gv.Policies, s.policyView(p, from, false))
		}
		if len(gv.Policies) > 0 {
			out = append(out, gv)
		}
	}
	return out
}

func (s *Site) renderPolicies(out map[string][]byte) {
	for _, p := range s.orderedPolicies() {
		v := s.newView(p.URL, p.Kind, p.Summary, "policies")
		v.Policy = s.policyView(p, p.URL, true)
		v.Nav = s.sectionNav("policies", p.URL)
		v.Prev, v.Next = prevNext(v.Nav, p.URL)
		v.EditURL = repoBlob + policiesFile
		v.TOC = []Heading{{Level: 2, ID: "targets", Text: "Behaviour per target"}}
		if len(p.Timeline) > 0 {
			v.TOC = append(v.TOC, Heading{Level: 2, ID: "timeline", Text: "Timeline"})
		}
		if len(p.Topics) > 0 {
			v.TOC = append(v.TOC, Heading{Level: 2, ID: "related-topics", Text: "Related topics"})
		}
		if len(p.Code) > 0 {
			v.TOC = append(v.TOC, Heading{Level: 2, ID: "source-files", Text: "Source files"})
		}
		s.exec(out, "policy", p.URL, v)
	}
}

// ---- landing page check ----

var landingHrefRe = regexp.MustCompile(`href="(docs/[^"#]*)(#[^"]*)?"`)

// checkLanding verifies that every docs/... link on docs/index.html resolves to a generated page.
func (s *Site) checkLanding() {
	b, err := os.ReadFile(filepath.Join(s.root, "docs", "index.html"))
	if err != nil {
		return
	}
	for _, m := range landingHrefRe.FindAllSubmatchIndex(b, -1) {
		href := string(b[m[2]:m[3]])
		url := strings.TrimPrefix(href, "docs/")
		if _, ok := s.urls[url]; !ok {
			line := 1 + bytes.Count(b[:m[0]], []byte("\n"))
			s.errorf("docs/index.html:%d: link %q does not match a generated page", line, href)
		}
	}
}

// ---- static files, CSS, search index, sitemap ----

func (s *Site) addStatic(out map[string][]byte) error {
	dir := filepath.Join(s.root, "site", "static")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out["static/"+filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		return fmt.Errorf("site/static: %v", err)
	}
	out["static/search-index.js"] = s.searchIndex()
	return nil
}

type searchEntry struct {
	Title    string      `json:"t"`
	Desc     string      `json:"d,omitempty"`
	URL      string      `json:"u"`
	Section  string      `json:"s"`
	Headings [][2]string `json:"h,omitempty"`
}

func (s *Site) searchIndex() []byte {
	var entries []searchEntry
	for _, p := range s.pages {
		e := searchEntry{Title: p.FM.Title, Desc: p.FM.Description, URL: p.URL, Section: sectionTitle(p.Section)}
		if p.Section == "" {
			e.Section = "Docs"
		}
		for _, h := range toc(p.headings) {
			e.Headings = append(e.Headings, [2]string{h.Text, h.ID})
		}
		entries = append(entries, e)
	}
	for _, p := range s.policies {
		entries = append(entries, searchEntry{Title: p.Kind, Desc: p.Summary, URL: p.URL, Section: "Policies"})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].URL < entries[j].URL })
	b, _ := json.Marshal(entries)
	return []byte("window.KM_SEARCH_INDEX = " + string(b) + ";\n")
}

func (s *Site) sitemap(lastmod string) []byte {
	urls := make([]string, 0, len(s.urls))
	for u := range s.urls {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	entry := func(loc, prio string) {
		fmt.Fprintf(&b, "  <url>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>monthly</changefreq>\n    <priority>%s</priority>\n  </url>\n", template.HTMLEscapeString(loc), lastmod, prio)
	}
	entry(siteURL, "1.0")
	for _, u := range urls {
		prio := "0.6"
		if u == "" {
			prio = "0.9"
		} else if strings.Count(u, "/") == 1 {
			prio = "0.8"
		}
		entry(docsURL+u, prio)
	}
	b.WriteString("</urlset>\n")
	return b.Bytes()
}
