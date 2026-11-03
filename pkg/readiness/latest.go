package readiness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Kong/kong-mesh-v3-readiness/preflight"
)

// The audit does not resolve the latest 2.x patch itself (it makes no request
// beyond the control plane). This lookup follows the kong-mesh-v3-preflight CLI
// (cmd/kong-mesh-v3-preflight/release.go, Apache-2.0): kumahq/kuma releases, which
// Kong Mesh patch numbers track, following pagination because GitHub orders
// releases by creation date.

// releasesURL is a var so tests can point it at a fake.
var releasesURL = "https://api.github.com/repos/kumahq/kuma/releases?per_page=100"

const (
	maxReleasePages = 20
	maxReleaseBytes = 16 << 20
)

// LatestPatch returns the highest released 2.<UpgradeTargetMinor>.x, e.g. "2.14.7".
func LatestPatch(ctx context.Context, hc *http.Client) (string, error) {
	best := -1
	next := releasesURL
	for page := 0; page < maxReleasePages && next != ""; page++ {
		patch, link, err := releasePage(ctx, hc, next)
		if err != nil {
			return "", err
		}
		best = max(best, patch)
		next = link
	}
	if next != "" {
		return "", fmt.Errorf("release list exceeded %d pages", maxReleasePages)
	}
	if best < 0 {
		return "", fmt.Errorf("no 2.%d.x release found", preflight.UpgradeTargetMinor)
	}
	return fmt.Sprintf("2.%d.%d", preflight.UpgradeTargetMinor, best), nil
}

func releasePage(ctx context.Context, hc *http.Client, url string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kuma-migrator")
	resp, err := hc.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("fetching GitHub releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("GitHub releases: status %d", resp.StatusCode)
	}
	var rels []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseBytes)).Decode(&rels); err != nil {
		return 0, "", fmt.Errorf("decoding GitHub releases: %w", err)
	}
	best := -1
	for _, r := range rels {
		if r.Draft || r.Prerelease {
			continue
		}
		maj, minor, patch, ok := preflight.ParseSemver(r.TagName)
		if ok && maj == 2 && minor == preflight.UpgradeTargetMinor {
			best = max(best, patch)
		}
	}
	return best, nextLink(resp.Header.Get("Link")), nil
}

// nextLink returns the rel="next" URL of a GitHub Link header, or "".
func nextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		seg := strings.Split(part, ";")
		if len(seg) < 2 {
			continue
		}
		isNext := false
		for _, p := range seg[1:] {
			if strings.TrimSpace(p) == `rel="next"` {
				isNext = true
			}
		}
		if !isNext {
			continue
		}
		u := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(seg[0]), "<"), ">")
		if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
			return u
		}
	}
	return ""
}
