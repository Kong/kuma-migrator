package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Policy is one entry of site/data/policies.yaml.
type Policy struct {
	Kind     string          `yaml:"kind"`
	Group    string          `yaml:"group"`
	Product  string          `yaml:"product"`
	Summary  string          `yaml:"summary"`
	V2       Target          `yaml:"v2"`
	V3       Target          `yaml:"v3"`
	Timeline []TimelineEntry `yaml:"timeline"`
	Topics   []string        `yaml:"topics"`
	Code     []string        `yaml:"code"`

	URL  string `yaml:"-"`
	line int
	node *yaml.Node
}

// Target is the behaviour for one --to-latest target.
type Target struct {
	Status string   `yaml:"status"`
	Output []string `yaml:"output"`
	Notes  string   `yaml:"notes"`
}

// TimelineEntry is one upstream change.
type TimelineEntry struct {
	Version string `yaml:"version"`
	Change  string `yaml:"change"`
}

// Policy groups in display order.
var policyGroups = []struct{ Key, Title string }{
	{"legacy", "Legacy policies"},
	{"mesh", "Mesh* policies"},
	{"kong-mesh", "Kong Mesh"},
	{"gateway", "Gateway"},
	{"resource", "Other resources"},
}

var validStatuses = []string{"converted", "rewritten", "passthrough", "warned", "error", "removed", "skipped"}

var validProducts = map[string]string{"kuma": "Kuma", "kong-mesh": "Kong Mesh"}

// Anchors that exist on every generated policy page.
var policyPageIDs = map[string]bool{"targets": true, "timeline": true, "related-topics": true, "source-files": true}

// Anchors the generated policy index adds (one per group).
var policyIndexIDs = func() map[string]bool {
	m := map[string]bool{}
	for _, g := range policyGroups {
		m[g.Key] = true
	}
	return m
}()

func groupTitle(key string) string {
	for _, g := range policyGroups {
		if g.Key == key {
			return g.Title
		}
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

const policiesFile = "site/data/policies.yaml"

// loadPolicies reads and validates site/data/policies.yaml. Topic references are
// validated later, once pages are loaded.
func (s *Site) loadPolicies() {
	raw, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(policiesFile)))
	if os.IsNotExist(err) {
		s.errorf("%s: missing (policy data for /docs/policies/)", policiesFile)
		return
	} else if err != nil {
		s.errorf("%s: %v", policiesFile, err)
		return
	}
	var list []*Policy
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&list); err != nil {
		s.errorf("%s: %v", policiesFile, err)
		return
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err == nil && len(root.Content) == 1 && root.Content[0].Kind == yaml.SequenceNode {
		for i, n := range root.Content[0].Content {
			if i < len(list) {
				list[i].line = n.Line
				list[i].node = n
			}
		}
	}
	for _, p := range list {
		at := fmt.Sprintf("%s:%d", policiesFile, p.line)
		if p.Kind == "" {
			s.errorf("%s: policy entry without kind", at)
			continue
		}
		at += " (" + p.Kind + ")"
		if groupTitle(p.Group) == "" {
			s.errorf("%s: unknown group %q (want one of legacy, mesh, kong-mesh, gateway, resource)", at, p.Group)
		}
		if _, ok := validProducts[p.Product]; !ok {
			s.errorf("%s: unknown product %q (want kuma or kong-mesh)", at, p.Product)
		}
		for _, t := range []struct {
			name string
			t    Target
		}{{"v2", p.V2}, {"v3", p.V3}} {
			if !contains(validStatuses, t.t.Status) {
				s.errorf("%s: unknown %s.status %q (want one of %s)", at, t.name, t.t.Status, strings.Join(validStatuses, ", "))
			}
		}
		for _, c := range p.Code {
			if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(c))); err != nil || strings.Contains(c, "..") {
				s.errorf("%s: code path %q does not exist in the repository", at, c)
			}
		}
		p.URL = "policies/" + strings.ToLower(p.Kind) + "/"
		if prev := s.policyByKind[p.Kind]; prev != nil {
			s.errorf("%s: duplicate kind (first defined at line %d)", at, prev.line)
			continue
		}
		s.policyByKind[p.Kind] = p
		s.policies = append(s.policies, p)
		s.claimURL(p.URL, at)
	}
	sort.SliceStable(s.policies, func(i, j int) bool { return s.policies[i].Kind < s.policies[j].Kind })
}

// valueLine returns the line where the markdown for a nested key starts.
func (p *Policy) valueLine(keys ...string) int {
	n := p.node
	for _, k := range keys {
		if n == nil || n.Kind != yaml.MappingNode {
			return p.line
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
			}
		}
		n = next
	}
	if n == nil {
		return p.line
	}
	if n.Style == yaml.LiteralStyle || n.Style == yaml.FoldedStyle {
		return n.Line + 1
	}
	return n.Line
}

func (s *Site) checkPolicyTopics() {
	for _, p := range s.policies {
		for _, t := range p.Topics {
			if s.bySrc["topics/"+t+".md"] == nil {
				s.errorf("%s:%d (%s): topic %q has no page site/content/topics/%s.md", policiesFile, p.valueLine("topics"), p.Kind, t, t)
			}
		}
	}
}

// policiesInGroup returns the policies of one group, sorted by kind.
func (s *Site) policiesInGroup(g string) []*Policy {
	var out []*Policy
	for _, p := range s.policies {
		if p.Group == g {
			out = append(out, p)
		}
	}
	return out
}

// orderedPolicies returns policies in navigation order (group, then kind).
func (s *Site) orderedPolicies() []*Policy {
	var out []*Policy
	for _, g := range policyGroups {
		out = append(out, s.policiesInGroup(g.Key)...)
	}
	return out
}
