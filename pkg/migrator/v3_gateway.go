package migrator

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kuma 3.0 changes to MeshHTTPRoute / MeshTCPRoute backendRefs. Both kinds are
// passthrough (they survive 3.0), so these run from ScanForDeprecations.

const (
	displayNameLabel   = "kuma.io/display-name"
	kubeNamespaceLabel = "k8s.kuma.io/namespace"
)

// fixRouteBackendRefsForV3 handles route backendRefs 3.0 no longer accepts
// (UPGRADE.md, "Route backendRef no longer accepts MeshServiceSubset"):
//
//   - backendRefs[].kind: MeshServiceSubset, and the same on a MeshHTTPRoute
//     RequestMirror filter's backendRef, is rejected on write; a stored one is
//     treated as unresolved and its traffic loses its destination.
//   - backendRefs[].tags is gone from the schema for every kind (pruned on
//     Kubernetes, ignored on Universal).
//
// Under v3, a MeshServiceSubset whose only selector is kuma.io/service (or its
// name) is a whole service, so it is rewritten to kind: MeshService selected by
// labels — 3.0 resolves real-resource refs by labels only. A subset with any
// other tag has no equivalent and is reported. Under v2 everything is reported
// as a forward-looking advisory and nothing is rewritten: MeshServiceSubset is
// valid on 2.14.
func fixRouteBackendRefsForV3(obj map[string]interface{}, name, kind string, target TargetVersion) (bool, []string) {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return false, nil
	}
	changed := false
	var warnings []string
	for ti, t := range asSlice(spec["to"]) {
		to, _ := t.(map[string]interface{})
		for ri, r := range asSlice(to["rules"]) {
			rule, _ := r.(map[string]interface{})
			def, _ := rule["default"].(map[string]interface{})
			if def == nil {
				continue
			}
			for bi, b := range asSlice(def["backendRefs"]) {
				ref, _ := b.(map[string]interface{})
				path := fmt.Sprintf("to[%d].rules[%d].default.backendRefs[%d]", ti, ri, bi)
				c, w := fixRouteBackendRef(ref, name, kind, path, target)
				changed = changed || c
				warnings = append(warnings, w...)
			}
			if kind != "MeshHTTPRoute" {
				continue
			}
			for fi, f := range asSlice(def["filters"]) {
				filter, _ := f.(map[string]interface{})
				mirror, _ := filter["requestMirror"].(map[string]interface{})
				ref, _ := mirror["backendRef"].(map[string]interface{})
				if ref == nil {
					continue
				}
				path := fmt.Sprintf("to[%d].rules[%d].default.filters[%d].requestMirror.backendRef", ti, ri, fi)
				c, w := fixRouteBackendRef(ref, name, kind, path, target)
				changed = changed || c
				warnings = append(warnings, w...)
			}
		}
	}
	return changed, warnings
}

// fixRouteBackendRef applies fixRouteBackendRefsForV3 to a single ref.
func fixRouteBackendRef(ref map[string]interface{}, name, kind, path string, target TargetVersion) (bool, []string) {
	if ref == nil {
		return false, nil
	}
	refKind, _ := ref["kind"].(string)
	tags := stringMap(ref["tags"])

	if refKind != "MeshServiceSubset" {
		if len(tags) == 0 {
			return false, nil
		}
		return false, []string{fmt.Sprintf(
			"%s %q: %s.tags (%s) %s select the backend with labels instead.",
			kind, name, path, formatTags(tags),
			target.removalNote("3.0 removes tags from the backendRef schema for every kind (pruned on Kubernetes, ignored on Universal) —"))}
	}

	svc := tags["kuma.io/service"]
	if svc == "" {
		svc, _ = ref["name"].(string)
	}
	var extra []string
	for k := range tags {
		if k != "kuma.io/service" {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)

	if !target.IsV3() || svc == "" || len(extra) > 0 || ref["labels"] != nil {
		why := "selecting a subset of endpoints by tag has no equivalent; split the destination into separate " +
			"MeshService resources and point the route at them."
		if len(extra) == 0 && svc != "" {
			why = "repoint it at kind: MeshService selected by labels (kuma.io/display-name, plus " +
				"k8s.kuma.io/namespace on Kubernetes) — --to-latest v3 does this automatically."
		}
		return false, []string{fmt.Sprintf(
			"%s %q: %s.kind is MeshServiceSubset (tags %s), %s %s",
			kind, name, path, formatTags(tags),
			target.removalNote("3.0 rejects it on write, and a stored one is treated as unresolved so its traffic loses its destination;"),
			why)}
	}

	svcName, svcNS := ParseKumaServiceTag(svc)
	if svcNS == "" {
		svcNS, _ = ref["namespace"].(string)
	}
	labels := map[string]interface{}{displayNameLabel: svcName}
	if svcNS != "" {
		labels[kubeNamespaceLabel] = svcNS
	}
	ref["kind"] = "MeshService"
	ref["labels"] = labels
	delete(ref, "tags")
	delete(ref, "name")
	delete(ref, "namespace")

	portNote := ""
	if _, hasPort := ref["port"]; !hasPort && ref["sectionName"] == nil {
		if p, err := strconv.Atoi(svcPortFromTag(svc)); err == nil && p > 0 {
			ref["port"] = p
		} else {
			portNote = " No port could be derived; add port or sectionName if the MeshService has more than one."
		}
	}

	return true, []string{fmt.Sprintf(
		"%s %q: %s rewritten from kind: MeshServiceSubset (kuma.io/service=%s) to kind: MeshService with "+
			"labels %s — Kuma 3.0 no longer accepts MeshServiceSubset in a route backendRef. Check that a "+
			"MeshService with these labels exists: on 3.0 a backendRef that resolves to nothing gets no split, "+
			"and a rule whose backendRefs all fail to resolve answers 500.%s",
		kind, name, path, svc, formatLabels(labels), portNote)}
}

func stringMap(v interface{}) map[string]string {
	m, _ := v.(map[string]interface{})
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = fmt.Sprint(val)
	}
	return out
}

func formatTags(tags map[string]string) string {
	parts := make([]string, 0, len(tags))
	for _, k := range sortedMapKeys(tags) {
		parts = append(parts, k+"="+tags[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatLabels(labels map[string]interface{}) string {
	parts := make([]string, 0, len(labels))
	for _, k := range sortedMapKeys(labels) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, labels[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
