package main

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The landing page docs/index.html is hand-written, but some regions of it are owned by the
// generator so that it matches the /docs/ pages while staying self-contained:
//
//	<!-- release-tag -->…<!-- /release-tag -->   the header's release badge (one line)
//	/* shared.css: … */ … /* /shared.css */       verbatim copy of site/static/shared.css
//	/* copy.js: … */ … /* /copy.js */             verbatim copy of site/static/copy.js
//
// The release-tag line is the only output that depends on git tags. `make site-check` ignores
// lines containing "release-tag" (as it ignores <lastmod>), so pushing a tag never makes the
// committed site stale; the next `make site` picks the new tag up.

const releasesLatest = repoURL + "/releases/latest"

// landingInlined lists the static files copied into marked regions of docs/index.html.
var landingInlined = []string{"shared.css", "copy.js"}

var (
	releaseRe = regexp.MustCompile(`<!-- release-tag -->.*?<!-- /release-tag -->`)
	tagRe     = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+-]*$`)
)

// gitRelease returns the latest tag reachable from HEAD, or "" (shallow clone, no tags).
func gitRelease(root string) string {
	cmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// releaseBadge returns the header badge, wrapped in its markers. An empty or malformed tag
// yields the markers alone, so the badge disappears instead of showing something wrong.
func releaseBadge(tag string) string {
	inner := ""
	if tagRe.MatchString(tag) {
		t := html.EscapeString(tag)
		inner = fmt.Sprintf(`<a class="release-badge" href="%s" title="Latest release: %s">%s</a>`, releasesLatest, t, t)
	}
	return "<!-- release-tag -->" + inner + "<!-- /release-tag -->"
}

// syncLanding rewrites the generator-owned regions of docs/index.html. Regions whose markers
// are absent are left alone.
func (s *Site) syncLanding() error {
	p := filepath.Join(s.root, "docs", "index.html")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	out := releaseRe.ReplaceAllLiteral(b, []byte(string(s.release)))
	for _, name := range landingInlined {
		q := regexp.QuoteMeta(name)
		re := regexp.MustCompile(`(?s)/\* ` + q + `:[^\n]*\*/\n(.*?)/\* /` + q + ` \*/`)
		m := re.FindSubmatchIndex(out)
		if m == nil {
			continue
		}
		src, err := os.ReadFile(filepath.Join(s.root, "site", "static", name))
		if err != nil {
			return fmt.Errorf("site/static/%s: %v", name, err)
		}
		if bytes.Contains(src, []byte("/* /"+name+" */")) {
			return fmt.Errorf("site/static/%s must not contain its own end marker", name)
		}
		if len(src) > 0 && src[len(src)-1] != '\n' {
			src = append(src, '\n')
		}
		var nb bytes.Buffer
		nb.Write(out[:m[2]])
		nb.Write(src)
		nb.Write(out[m[3]:])
		out = nb.Bytes()
	}
	if bytes.Equal(out, b) {
		return nil
	}
	return os.WriteFile(p, out, 0o644)
}
