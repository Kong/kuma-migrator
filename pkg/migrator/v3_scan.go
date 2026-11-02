package migrator

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// Kuma 3.0 checks for MeshService, MeshPassthrough, ZoneIngress/ZoneEgress,
// ContainerPatch and the reachable-services/backends workload annotations.
// UPGRADE.md "Upgrade to 3.0.0" is the source for each.

// ---- MeshService (3.0) -------------------------------------------------------

// fixMeshService handles the 3.0 MeshService removals:
//   - spec.selector.dataplaneTags → spec.selector.dataplaneLabels.matchLabels
//     ("dataplaneTags removed from the MeshService selector"). Un-migrated selectors
//     are dropped silently and the MeshService matches no proxies.
//   - spec.identities[] type ServiceTag ("MeshService.spec.identities now accepts
//     SPIFFE IDs only"). The SPIFFE ID cannot be derived from the tag, so those
//     entries are dropped.
//
// Both rewrites are v3 only. 2.14 has dataplaneLabels, but it matches Dataplane
// labels, and on 2.x Universal the keys usually exist only as inbound tags — the
// v3 Dataplane rewrite is what moves them to labels. Under v2 an advisory is given.
func fixMeshService(obj map[string]interface{}, name string, target TargetVersion) (bool, []string) {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return false, nil
	}
	changed := false
	var warnings []string

	if selector, ok := spec["selector"].(map[string]interface{}); ok {
		if tags, ok := selector["dataplaneTags"].(map[string]interface{}); ok && len(tags) > 0 {
			c, ws := fixMeshServiceDataplaneTags(selector, tags, name, target)
			changed = changed || c
			warnings = append(warnings, ws...)
		}
	}

	if target.IsV3() {
		if ids, ok := spec["identities"].([]interface{}); ok {
			var kept []interface{}
			var dropped []string
			for _, id := range ids {
				m, _ := id.(map[string]interface{})
				if t, _ := m["type"].(string); t == "ServiceTag" {
					dropped = append(dropped, fmt.Sprint(m["value"]))
					continue
				}
				kept = append(kept, id)
			}
			if len(dropped) > 0 {
				if len(kept) == 0 {
					delete(spec, "identities")
				} else {
					spec["identities"] = kept
				}
				changed = true
				warnings = append(warnings, fmt.Sprintf(
					"MeshService %q: spec.identities[] type ServiceTag is rejected in 3.0 — auto-removed (%s). "+
						"3.0 publishes SPIFFE IDs only; add type: SpiffeID entries if clients need the identity "+
						"(it cannot be derived from the service tag).",
					name, strings.Join(dropped, ", ")))
			}
		}
	}
	return changed, warnings
}

func fixMeshServiceDataplaneTags(selector, tags map[string]interface{}, name string, target TargetVersion) (bool, []string) {
	var untranslatable []string
	for _, k := range sortedMapKeys(tags) {
		// Inbound-tag-only keys under the reserved prefixes have no label
		// counterpart on a 3.0 Dataplane, except the ones the CP writes itself.
		if isReservedLabelPrefix(k) && k != labelK8sNamespace && k != "kuma.io/zone" && k != "kuma.io/mesh" {
			untranslatable = append(untranslatable, fmt.Sprintf("%s=%v", k, tags[k]))
		}
	}
	if !target.IsV3() {
		return false, []string{fmt.Sprintf(
			"MeshService %q: spec.selector.dataplaneTags is removed in 3.0 and dropped silently — the "+
				"MeshService then matches no proxies and goes Unavailable. Switch to "+
				"spec.selector.dataplaneLabels.matchLabels once the selected Dataplanes carry those keys as "+
				"labels (2.14 supports dataplaneLabels). Re-run with --to-latest v3 to rewrite it.",
			name)}
	}
	if len(untranslatable) > 0 {
		return false, []string{fmt.Sprintf(
			"MeshService %q: spec.selector.dataplaneTags is removed in 3.0 and dropped silently, leaving a "+
				"MeshService that matches no proxies. Not converted: %s cannot be a Dataplane label (reserved "+
				"kuma.io/ prefix), and dropping it would widen the selector. Rewrite as "+
				"spec.selector.dataplaneLabels.matchLabels on labels the Dataplanes actually carry.",
			name, strings.Join(untranslatable, ", "))}
	}

	dl, _ := selector["dataplaneLabels"].(map[string]interface{})
	if dl == nil {
		dl = map[string]interface{}{}
	}
	ml, _ := dl["matchLabels"].(map[string]interface{})
	if ml == nil {
		ml = map[string]interface{}{}
	}
	res := mergeTagsIntoLabels(ml, tags)
	if len(res.conflicts) > 0 {
		return false, []string{fmt.Sprintf(
			"MeshService %q: spec.selector.dataplaneTags conflicts with dataplaneLabels.matchLabels (%s). "+
				"dataplaneTags is removed in 3.0; reconcile the two selectors by hand.",
			name, strings.Join(res.conflicts, ", "))}
	}
	dl["matchLabels"] = ml
	selector["dataplaneLabels"] = dl
	delete(selector, "dataplaneTags")
	var moved []string
	for _, k := range sortedMapKeys(tags) {
		moved = append(moved, fmt.Sprintf("%s=%v", k, tags[k]))
	}
	return true, []string{fmt.Sprintf(
		"MeshService %q: spec.selector.dataplaneTags is removed in 3.0 — auto-converted to "+
			"dataplaneLabels.matchLabels (%s). Inbound tags no longer identify a proxy, so these keys must "+
			"exist as labels on the Dataplane itself: Pod labels on Kubernetes, top-level labels on Universal.",
		name, strings.Join(moved, ", "))}
}

// ---- MeshPassthrough (3.0) ---------------------------------------------------

// warnMeshPassthroughV3 covers the two MeshPassthrough validation changes in 3.0:
//
//   - A non-wildcard Domain match needs a port ("MeshPassthrough resolves a domain
//     match itself and needs a port for it"). v3 only: port is optional on 2.x.
//   - Matches that resolve to the same Envoy filter chain are rejected ("MeshPassthrough
//     rejects matches that resolve to the same Envoy filter chain"). Reported on both
//     targets: on 2.x such a policy was accepted and Envoy rejected the whole
//     passthrough listener.
//
// The collision check ports FindConflicts from
// kuma/pkg/plugins/policies/meshpassthrough/api/v1alpha1/conflicts.go: two matches
// collide when they share a filter chain key (protocol class, port, normalized
// address, SNI for tls) without sharing a chain owner (same protocol and, except for
// L7 domains, same value).
func warnMeshPassthroughV3(obj map[string]interface{}, name string, target TargetVersion) []string {
	var matches []map[string]interface{}
	forEachPassthroughMatch(obj, func(m map[string]interface{}) { matches = append(matches, m) })
	if len(matches) == 0 {
		return nil
	}
	var warnings []string

	if target.IsV3() {
		for _, m := range matches {
			typ, _ := m["type"].(string)
			value, _ := m["value"].(string)
			if typ != "Domain" || strings.HasPrefix(value, "*") {
				continue
			}
			if p, ok := intValue(m["port"]); ok && p != 0 {
				continue
			}
			protocol, _ := m["protocol"].(string)
			warnings = append(warnings, fmt.Sprintf(
				"MeshPassthrough %q: appendMatch Domain %q (protocol %s) has no port. 3.0 resolves the domain in "+
					"the sidecar and needs a port: the policy is rejected on apply, and a stored policy stops "+
					"applying this match (if it is the only one, all passthrough traffic is rejected). Add "+
					"`port:` — duplicate the match for each port the domain is used on.",
				name, value, protocol))
		}
	}

	type owner struct {
		protocol, chainValue, value string
	}
	owners := map[string]owner{}
	for _, m := range matches {
		typ, _ := m["type"].(string)
		value, _ := m["value"].(string)
		protocol, _ := m["protocol"].(string)
		port, _ := intValue(m["port"])
		if passthroughMatchInvalid(typ, value, port) {
			continue
		}
		key, desc := passthroughChainKey(typ, value, protocol, port)
		cand := owner{protocol: protocol, chainValue: value, value: value}
		if typ == "Domain" && l7ProtocolsNeedingPort[protocol] {
			cand.chainValue = ""
		}
		prev, used := owners[key]
		switch {
		case !used:
			owners[key] = cand
		case prev.protocol == cand.protocol && prev.chainValue == cand.chainValue:
			// same chain, merges
		case prev.protocol != cand.protocol:
			warnings = append(warnings, fmt.Sprintf(
				"MeshPassthrough %q: protocols %s and %s produce the same Envoy filter chain for %s (match %q). "+
					"3.0 rejects the policy on its next edit and drops the later match at runtime; on 2.x Envoy "+
					"rejects the whole passthrough listener. Pick one protocol per port.",
				name, prev.protocol, cand.protocol, desc, value))
		default:
			warnings = append(warnings, fmt.Sprintf(
				"MeshPassthrough %q: matches %q and %q produce the same Envoy filter chain for %s — the same "+
					"address written two ways. 3.0 rejects the policy on its next edit and drops the later match "+
					"at runtime; on 2.x Envoy rejects the whole passthrough listener. Keep one spelling.",
				name, prev.value, cand.value, desc))
		}
	}
	return warnings
}

// passthroughMatchInvalid mirrors invalidMatch upstream: matches the generator
// cannot build a chain from are left to other validation.
func passthroughMatchInvalid(typ, value string, port int) bool {
	switch typ {
	case "IP":
		return net.ParseIP(value) == nil
	case "CIDR":
		_, _, err := net.ParseCIDR(value)
		return err != nil
	case "Domain":
		return port == 0 && !strings.HasPrefix(value, "*")
	}
	return true
}

// passthroughChainKey mirrors FilterChainMatcher upstream.
func passthroughChainKey(typ, value, protocol string, port int) (key, desc string) {
	class := "http"
	switch protocol {
	case "tls":
		class = "tls"
	case "tcp", "mysql":
		class = "tcp"
	}
	var address, sni string
	ipv6 := false
	switch typ {
	case "IP":
		ipv6 = net.ParseIP(value) != nil && strings.Contains(value, ":")
		if ipv6 {
			address = canonicalCIDR(value + "/128")
		} else {
			address = canonicalCIDR(value + "/32")
		}
	case "CIDR":
		address = canonicalCIDR(value)
		a, _, _ := strings.Cut(address, "/")
		ipv6 = strings.Contains(a, ":")
	case "Domain":
		if class == "tls" {
			sni = value
		}
	}
	key = fmt.Sprintf("%s|%d|%s|%v|%s", class, port, address, ipv6, sni)
	chain := "domains"
	switch {
	case sni != "":
		chain = fmt.Sprintf("domain %q", sni)
	case address != "":
		chain = address
	}
	if port == 0 {
		return key, chain + " on all ports"
	}
	return key, fmt.Sprintf("%s on port %d", chain, port)
}

func canonicalCIDR(value string) string {
	if _, ipNet, err := net.ParseCIDR(value); err == nil {
		return ipNet.String()
	}
	return value
}

// ---- ZoneIngress / ZoneEgress (3.0) ------------------------------------------

// warnZoneProxyRemoved reports standalone ZoneIngress/ZoneEgress resources under
// v3. 3.0 removes the resources, their CRDs, REST endpoints and KDS sync
// ("ZoneIngress and ZoneEgress resources removed"), and stops serving
// kuma-dp --proxy-type=ingress|egress ("Standalone ZoneIngress/ZoneEgress proxies are
// no longer supported"). The replacement is a mesh-scoped zone proxy: a regular
// Dataplane with networking.listeners[] of type ZoneIngress/ZoneEgress, one per
// mesh. Not converted: the target mesh(es) and the dataplane token are not in the
// manifest, and the migration must happen before the zone CP upgrade.
//
// For ZoneIngress the advisory also covers "Cross-zone MeshService routing requires
// MeshZoneAddress": the advertised address is no longer read, and the
// MeshZoneAddress that replaces it cannot exist before 3.0, so it is a post-upgrade
// step. The scan pass emits one document per input, so the MeshZoneAddress is given
// as a snippet rather than generated.
//
// Under v2 the same removal is a forward-looking advisory: the mesh-scoped zone
// proxies already exist on 2.14 (Helm meshes[].ingress/egress, Dataplane
// networking.listeners[]), so the move can be made before the 3.0 upgrade.
func warnZoneProxyRemoved(obj map[string]interface{}, name, kind string, target TargetVersion) []string {
	netw := dataplaneNetworking(obj)
	if !target.IsV3() {
		return []string{fmt.Sprintf(
			"%s %q: the %s resource is removed in 3.0 (CRD, API and KDS sync), and a 3.0 control plane no "+
				"longer serves kuma-dp --proxy-type=ingress|egress. 2.14 already supports the replacement, "+
				"mesh-scoped zone proxies: on Kubernetes enable meshes[].ingress / meshes[].egress in the Helm "+
				"values; on Universal run one regular Dataplane per mesh with networking.listeners[] of type %s. "+
				"Move to them and delete this %s before upgrading the zone control plane to 3.0. Re-run with "+
				"--to-latest v3 for the full checklist (including MeshZoneAddress for cross-zone traffic).",
			kind, name, kind, kind, kind)}
	}
	addr, _ := netw["address"].(string)
	if addr == "" {
		addr = "<address>"
	}
	port := "<port>"
	if p, ok := intValue(netw["port"]); ok {
		port = fmt.Sprint(p)
	}
	listenerType := kind

	msg := fmt.Sprintf(
		"BLOCKING — %s %q: the %s resource is removed in 3.0 (CRD, API and KDS sync), and the control plane "+
			"no longer serves kuma-dp --proxy-type=ingress|egress. Not converted. Before upgrading the zone "+
			"control plane: deploy mesh-scoped zone proxies (Kubernetes: meshes[].ingress.enabled / "+
			"meshes[].egress.enabled; Universal: one regular Dataplane per mesh with "+
			"`networking: {address: %s, listeners: [{type: %s, address: %s, port: %s}]}`, its token reissued "+
			"without --proxy-type), then delete this %s. On Kubernetes, after upgrading, delete the CRDs: "+
			"kubectl delete crd zoneingresses.kuma.io zoneegresses.kuma.io zoneingressinsights.kuma.io "+
			"zoneegressinsights.kuma.io.",
		kind, name, kind, addr, listenerType, addr, port, kind)
	warnings := []string{msg}

	if kind == "ZoneIngress" {
		advAddr, _ := netw["advertisedAddress"].(string)
		if advAddr == "" {
			advAddr = addr
		}
		advPort := port
		if p, ok := intValue(netw["advertisedPort"]); ok {
			advPort = fmt.Sprint(p)
		}
		warnings = append(warnings, fmt.Sprintf(
			"ZoneIngress %q: 3.0 takes a zone's public address only from MeshZoneAddress, not from the "+
				"ZoneIngress advertisedAddress/advertisedPort. Cross-zone MeshService and MeshMultiZoneService "+
				"traffic to this zone is down until it exists. On Kubernetes the control plane creates it for each "+
				"mesh-scoped zone proxy Service; on Universal, right after upgrading the zone control plane, apply "+
				"one per mesh served cross-zone: `type: MeshZoneAddress, name: zone-proxy-ingress, mesh: <mesh>, "+
				"labels: {kuma.io/origin: zone}, spec: {address: %s, port: %s}`.",
			name, advAddr, advPort))
	}
	return warnings
}

// ---- ContainerPatch sidecar probes (3.0) -------------------------------------

var adminPortRe = regexp.MustCompile(`\b9901\b`)

// warnContainerPatchAdminProbePort flags a ContainerPatch that points the sidecar's
// probes at the Envoy admin port 9901. 3.0 always probes the kuma-dp readiness port
// 9902 (bootstrapServer.params.readinessPort), whatever the admin transport
// ("Kubernetes probes on the sidecar always use the readiness port"). v3 only:
// on 2.x the probes use 9901 when the admin API runs over TCP.
func warnContainerPatchAdminProbePort(obj map[string]interface{}, name string, target TargetVersion) []string {
	if !target.IsV3() {
		return nil
	}
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return nil
	}
	var hits []string
	for _, section := range []string{"sidecarPatch", "initPatch"} {
		patches, _ := spec[section].([]interface{})
		for i, p := range patches {
			m, _ := p.(map[string]interface{})
			path, _ := m["path"].(string)
			value := fmt.Sprint(m["value"])
			probePath := strings.Contains(strings.ToLower(path), "probe")
			probeValue := strings.Contains(strings.ToLower(value), "probe")
			if (probePath || probeValue) && adminPortRe.MatchString(value) {
				hits = append(hits, fmt.Sprintf("%s[%d] (%s)", section, i, path))
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"ContainerPatch %q: %s points sidecar probes at the Envoy admin port 9901. 3.0 always probes the "+
			"readiness port 9902 (or bootstrapServer.params.readinessPort if changed) and proxies /ready to "+
			"Envoy there. Change the port to 9902.",
		name, strings.Join(hits, ", "))}
}

// ---- workload annotations: reachable services / backends (3.0) ---------------

const (
	annotationReachableBackends = "kuma.io/reachable-backends"
	annotationReachableServices = "kuma.io/transparent-proxying-reachable-services"
)

// fixWorkloadReachableAnnotations rewrites the reachable-* annotations on a Pod or
// a workload's pod template:
//   - kuma.io/reachable-backends refs[].name/namespace → labels (v3; the 3.0 pod
//     converter rejects an annotation that still sets them).
//   - kuma.io/transparent-proxying-reachable-services → kuma.io/reachable-backends
//     for <name>_<ns>_svc_<port> entries (v3; the annotation is removed in 3.0).
//
// Under v2 only an advisory for name/namespace refs is returned.
func fixWorkloadReachableAnnotations(obj map[string]interface{}, name, kind string, target TargetVersion) (bool, []string) {
	changed := false
	var warnings []string
	for _, site := range podAnnotationSites(obj) {
		ns := site.namespace
		c, ws := fixReachableAnnotationsAt(site.annotations, kind, name, site.path, ns, target)
		changed = changed || c
		warnings = append(warnings, ws...)
	}
	return changed, warnings
}

func fixReachableAnnotationsAt(anns map[string]interface{}, kind, name, where, ns string, target TargetVersion) (bool, []string) {
	changed := false
	var warnings []string

	var doc map[string]interface{}
	if raw, ok := anns[annotationReachableBackends].(string); ok {
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
			return false, []string{fmt.Sprintf("%s %q: %s annotation %s is not valid YAML: %v",
				kind, name, where, annotationReachableBackends, err)}
		}
	}
	refs, _ := doc["refs"].([]interface{})

	if !target.IsV3() {
		return false, warnReachableBackendsNameRefsV2(refs, kind, name,
			fmt.Sprintf("the %s annotation (%s)", annotationReachableBackends, where))
	}

	docChanged := false
	if c, notes := fixReachableBackendRefsForV3(refs, ns); c {
		docChanged = true
		warnings = append(warnings, fmt.Sprintf(
			"%s %q: %s annotation %s refs select by labels only in 3.0 — the pod converter rejects name and "+
				"namespace. Auto-rewritten: name → labels[%s], namespace → labels[%s].%s",
			kind, name, where, annotationReachableBackends, labelDisplayName, labelK8sNamespace, joinNotes(notes)))
	}

	if raw, ok := anns[annotationReachableServices].(string); ok {
		var services []string
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				services = append(services, s)
			}
		}
		newRefs, rest := convertReachableServices(services)
		if len(newRefs) > 0 {
			refs = appendReachableRefs(refs, newRefs)
			docChanged = true
			if len(rest) == 0 {
				delete(anns, annotationReachableServices)
			} else {
				anns[annotationReachableServices] = strings.Join(rest, ",")
			}
			changed = true
			warnings = append(warnings, fmt.Sprintf(
				"%s %q: %s annotation %s is removed in 3.0. Auto-converted to %s: %s.",
				kind, name, where, annotationReachableServices, annotationReachableBackends, describeRefs(newRefs)))
		}
	}

	if docChanged {
		if doc == nil {
			doc = map[string]interface{}{}
		}
		doc["refs"] = refs
		out, err := yaml.Marshal(doc)
		if err == nil {
			anns[annotationReachableBackends] = string(out)
			changed = true
		}
	}
	return changed, warnings
}

// ---- annotation sites --------------------------------------------------------

// annotationSite is one annotations map on a resource: the object's own
// metadata.annotations, or a pod template's.
type annotationSite struct {
	path        string // e.g. "metadata.annotations", "spec.template.metadata.annotations"
	annotations map[string]interface{}
	namespace   string // namespace the pod would run in, when known
}

// podAnnotationSites returns every annotations map that can carry Kuma pod
// annotations: metadata.annotations, spec.template.metadata.annotations
// (Deployment/StatefulSet/DaemonSet/ReplicaSet/Job) and
// spec.jobTemplate.spec.template.metadata.annotations (CronJob).
func podAnnotationSites(obj map[string]interface{}) []annotationSite {
	meta, _ := obj["metadata"].(map[string]interface{})
	ns, _ := meta["namespace"].(string)
	var sites []annotationSite
	add := func(path string, m map[string]interface{}) {
		md, _ := m["metadata"].(map[string]interface{})
		anns, _ := md["annotations"].(map[string]interface{})
		if len(anns) > 0 {
			sites = append(sites, annotationSite{path: path, annotations: anns, namespace: ns})
		}
	}
	add("metadata.annotations", obj)
	spec, _ := obj["spec"].(map[string]interface{})
	if tmpl, ok := spec["template"].(map[string]interface{}); ok {
		add("spec.template.metadata.annotations", tmpl)
	}
	if jt, ok := spec["jobTemplate"].(map[string]interface{}); ok {
		jspec, _ := jt["spec"].(map[string]interface{})
		if tmpl, ok := jspec["template"].(map[string]interface{}); ok {
			add("spec.jobTemplate.spec.template.metadata.annotations", tmpl)
		}
	}
	return sites
}
