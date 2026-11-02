package migrator

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kuma 3.0 Dataplane rewrites and removals (UPGRADE.md "Upgrade to 3.0.0").
//
// Every rewrite in this file runs only under --to-latest v3: the fields it removes
// (inbound tags, networking.gateway, tag-based outbounds, reachableServices) are
// still required or still valid on 2.x, so stripping them from v2 output would
// break a 2.x control plane. The single exception is the reachableBackends
// name/namespace advisory, which fires under v2 because 2.14 already accepts the
// labels form and the operator can switch early.

// Labels the control plane writes on a Dataplane itself. Moving a tag onto one of
// these is never valid, so they are reported rather than copied.
const (
	labelDisplayName  = "kuma.io/display-name"
	labelK8sNamespace = "k8s.kuma.io/namespace"
)

// fixDataplane applies the Kuma 3.0 Dataplane rewrites (v3 only) and returns the
// notes for each. Order matters:
//  1. inbound protocol is copied from the kuma.io/protocol tag before the tags go;
//  2. inbound tags move to labels;
//  3. networking.gateway goes, with its tags moved to labels;
//  4. tag-based outbounds become backendRef outbounds;
//  5. reachableServices entries become reachableBackends refs;
//  6. reachableBackends refs move from name/namespace to labels.
//
// Under v2 only the reachableBackends name/namespace advisory is returned.
func fixDataplane(obj map[string]interface{}, name string, target TargetVersion) (bool, []string) {
	if !target.IsV3() {
		return false, warnReachableBackendsNameRefsV2(dataplaneReachableBackendRefs(obj), "Dataplane", name,
			"transparentProxying.reachableBackends.refs[]")
	}
	changed := false
	var warnings []string
	run := func(c bool, ws []string) {
		changed = changed || c
		warnings = append(warnings, ws...)
	}
	run(fixDataplaneInboundProtocolForV3(obj, name))
	run(fixDataplaneInboundTagsForV3(obj, name))
	run(fixDataplaneGatewayForV3(obj, name))
	run(fixDataplaneOutboundTagsForV3(obj, name))
	run(fixDataplaneReachableServicesForV3(obj, name))
	if refs := dataplaneReachableBackendRefs(obj); refs != nil {
		c, notes := fixReachableBackendRefsForV3(refs, dataplaneNamespace(obj))
		if c {
			warnings = append(warnings, fmt.Sprintf(
				"Dataplane %q: transparentProxying.reachableBackends.refs[] selects by labels only in 3.0 — "+
					"refs that still use name/namespace resolve to nothing and the proxy gets no outbounds. "+
					"Auto-rewritten: name → labels[%s], namespace → labels[%s].%s",
				name, labelDisplayName, labelK8sNamespace, joinNotes(notes)))
		}
		changed = changed || c
	}
	return changed, warnings
}

// ---- shape helpers -----------------------------------------------------------

// dataplaneNetworking returns the networking block of a Dataplane: top-level on
// Universal, under spec on Kubernetes.
func dataplaneNetworking(obj map[string]interface{}) map[string]interface{} {
	if n, ok := obj["networking"].(map[string]interface{}); ok {
		return n
	}
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		if n, ok := spec["networking"].(map[string]interface{}); ok {
			return n
		}
	}
	return nil
}

// isKubernetesShape reports whether obj uses the Kubernetes layout (kind +
// metadata) rather than the Universal one (type + top-level name/labels).
func isKubernetesShape(obj map[string]interface{}) bool {
	k, _ := obj["kind"].(string)
	return k != ""
}

// resourceLabelMap returns the labels of obj: metadata.labels on Kubernetes,
// top-level labels on Universal. With create, a missing map is created in place.
func resourceLabelMap(obj map[string]interface{}, create bool) map[string]interface{} {
	parent := obj
	if isKubernetesShape(obj) {
		meta, ok := obj["metadata"].(map[string]interface{})
		if !ok {
			if !create {
				return nil
			}
			meta = map[string]interface{}{}
			obj["metadata"] = meta
		}
		parent = meta
	}
	labels, ok := parent["labels"].(map[string]interface{})
	if !ok && create {
		labels = map[string]interface{}{}
		parent["labels"] = labels
	}
	return labels
}

// dataplaneNamespace returns metadata.namespace (Kubernetes layout), or "".
func dataplaneNamespace(obj map[string]interface{}) string {
	meta, _ := obj["metadata"].(map[string]interface{})
	ns, _ := meta["namespace"].(string)
	return ns
}

// isReservedLabelPrefix reports whether k sits under a prefix Kuma 3.0 reserves
// ("Reserved label prefixes"): unknown labels there are rejected on write, and a
// kuma-dp Dataplane carrying one fails to register.
func isReservedLabelPrefix(k string) bool {
	return strings.HasPrefix(k, "kuma.io/") || strings.HasPrefix(k, "k8s.kuma.io/")
}

// labelMerge records the outcome of copying tags into a label map.
type labelMerge struct {
	moved     []string // k=v copied into labels
	reserved  []string // k=v under a reserved prefix, not copied
	conflicts []string // k: existing label value differs, not copied
}

// mergeTagsIntoLabels copies every tag outside the reserved prefixes into labels.
// An existing label with a different value is never overwritten.
func mergeTagsIntoLabels(labels, tags map[string]interface{}) labelMerge {
	var res labelMerge
	for _, k := range sortedMapKeys(tags) {
		v := fmt.Sprint(tags[k])
		if isReservedLabelPrefix(k) {
			res.reserved = append(res.reserved, k+"="+v)
			continue
		}
		if existing, ok := labels[k]; ok {
			if fmt.Sprint(existing) != v {
				res.conflicts = append(res.conflicts, fmt.Sprintf("%s (label %q, tag %q)", k, fmt.Sprint(existing), v))
			}
			continue
		}
		labels[k] = v
		res.moved = append(res.moved, k+"="+v)
	}
	return res
}

func sortedMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinNotes(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return " " + strings.Join(notes, " ")
}

// intValue converts a YAML-decoded number (float64 via sigs.k8s.io/yaml), int or
// numeric string to an int.
func intValue(v interface{}) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), t == float64(int(t))
	case int:
		return t, true
	case int64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(t)
		return n, err == nil
	}
	return 0, false
}

// decodeServiceTagPort decodes a Kubernetes-style kuma.io/service value
// "<name>_<namespace>_svc_<port>" into its parts. Plain Universal values carry no
// namespace or port and return ok=false.
func decodeServiceTagPort(value string) (name, namespace string, port int, ok bool) {
	idx := strings.LastIndex(value, "_svc_")
	if idx == -1 {
		return "", "", 0, false
	}
	port, err := strconv.Atoi(value[idx+len("_svc_"):])
	if err != nil || port < 1 || port > 65535 {
		return "", "", 0, false
	}
	name, namespace = ParseKumaServiceTag(value)
	if name == "" || namespace == "" {
		return "", "", 0, false
	}
	return name, namespace, port, true
}

// meshServiceRefLabels is the 3.0 labels selector for a MeshService name/namespace.
func meshServiceRefLabels(name, namespace string) map[string]interface{} {
	labels := map[string]interface{}{labelDisplayName: name}
	if namespace != "" {
		labels[labelK8sNamespace] = namespace
	}
	return labels
}

func inboundList(net map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	ins, _ := net["inbound"].([]interface{})
	for _, in := range ins {
		if m, ok := in.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func inboundLabel(in map[string]interface{}, idx int) string {
	if p, ok := intValue(in["port"]); ok {
		return fmt.Sprintf("inbound port %d", p)
	}
	return fmt.Sprintf("inbound[%d]", idx)
}

// ---- inbound protocol from kuma.io/protocol (3.0) -----------------------------

// fixDataplaneInboundProtocolForV3 copies inbound[].tags["kuma.io/protocol"] into
// inbound[].protocol when the field is unset. 3.0 reads the protocol only from the
// field; an inbound without it is served as plain TCP and loses every L7 filter,
// with nothing rejecting the resource (UPGRADE.md "kuma.io/protocol inbound tag no
// longer sets the protocol").
func fixDataplaneInboundProtocolForV3(obj map[string]interface{}, name string) (bool, []string) {
	net := dataplaneNetworking(obj)
	if net == nil {
		return false, nil
	}
	var set, mismatched []string
	for i, in := range inboundList(net) {
		tags, _ := in["tags"].(map[string]interface{})
		tagProto, _ := tags["kuma.io/protocol"].(string)
		if tagProto == "" {
			continue
		}
		current, _ := in["protocol"].(string)
		switch {
		case current == "":
			in["protocol"] = tagProto
			set = append(set, fmt.Sprintf("%s=%s", inboundLabel(in, i), tagProto))
		case current != tagProto:
			mismatched = append(mismatched, fmt.Sprintf("%s (protocol %q, tag %q)", inboundLabel(in, i), current, tagProto))
		}
	}
	var warnings []string
	if len(set) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Dataplane %q: 3.0 no longer reads the inbound protocol from the kuma.io/protocol tag — an "+
				"inbound without networking.inbound[].protocol is served as plain TCP and loses its L7 "+
				"filters, silently. Auto-set inbound[].protocol from the tag: %s.",
			name, strings.Join(set, ", ")))
	}
	if len(mismatched) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Dataplane %q: inbound protocol field and kuma.io/protocol tag disagree on %s. 3.0 uses only the "+
				"field; check it is the protocol you want.",
			name, strings.Join(mismatched, ", ")))
	}
	return len(set) > 0, warnings
}

// ---- inbound tags → Dataplane labels (3.0) -----------------------------------

// fixDataplaneInboundTagsForV3 moves inbound[].tags onto the Dataplane's labels
// (top-level labels on Universal, metadata.labels on Kubernetes) and removes the
// tags. Keys under kuma.io/ and k8s.kuma.io/ are reserved label prefixes in 3.0 and
// cannot be labels, so they are dropped and listed. An inbound whose tags conflict
// with an existing label keeps its tags, so warnDataplaneInboundTags still flags it.
func fixDataplaneInboundTagsForV3(obj map[string]interface{}, name string) (bool, []string) {
	net := dataplaneNetworking(obj)
	if net == nil {
		return false, nil
	}
	var moved, reserved, conflicts []string
	changed := false
	for i, in := range inboundList(net) {
		tags, ok := in["tags"].(map[string]interface{})
		if !ok {
			continue
		}
		if len(tags) == 0 {
			delete(in, "tags")
			changed = true
			continue
		}
		labels := resourceLabelMap(obj, true)
		res := mergeTagsIntoLabels(labels, tags)
		if len(labels) == 0 {
			// Nothing was copied and the map was created for nothing.
			deleteEmptyLabelMap(obj)
		}
		moved = append(moved, res.moved...)
		reserved = append(reserved, res.reserved...)
		if len(res.conflicts) > 0 {
			conflicts = append(conflicts, fmt.Sprintf("%s: %s", inboundLabel(in, i), strings.Join(res.conflicts, ", ")))
			if len(res.moved) > 0 {
				changed = true
			}
			continue
		}
		delete(in, "tags")
		changed = true
	}
	if !changed && len(conflicts) == 0 {
		return false, nil
	}

	msg := fmt.Sprintf("Dataplane %q: networking.inbound[].tags is removed in 3.0 and dropped silently on apply, "+
		"so anything selecting on those tags stops matching.", name)
	if len(moved) > 0 {
		msg += " Auto-moved to Dataplane labels: " + strings.Join(dedupe(moved), ", ") + "."
	}
	if len(reserved) > 0 {
		msg += " Dropped, because kuma.io/ and k8s.kuma.io/ are reserved label prefixes: " +
			strings.Join(dedupe(reserved), ", ") + ". Policies or MeshService selectors that matched on " +
			"these must select on a label or the MeshService instead."
	}
	warnings := []string{msg}
	if len(conflicts) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Dataplane %q: inbound tags conflict with existing labels and were left in place — %s. Labels "+
				"are per Dataplane, not per inbound: pick one value and remove the tags by hand.",
			name, strings.Join(conflicts, "; ")))
	}
	return changed, warnings
}

func deleteEmptyLabelMap(obj map[string]interface{}) {
	parent := obj
	if isKubernetesShape(obj) {
		meta, ok := obj["metadata"].(map[string]interface{})
		if !ok {
			return
		}
		parent = meta
	}
	if l, ok := parent["labels"].(map[string]interface{}); ok && len(l) == 0 {
		delete(parent, "labels")
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- networking.gateway and the kuma.io/gateway label (3.0) -------------------

// fixDataplaneGatewayForV3 removes networking.gateway (any type) and the
// kuma.io/gateway / kuma.io/proxy-type labels.
//
// 3.0 removes the gateway marking entirely (UPGRADE.md "kuma.io/gateway is removed",
// "The BUILTIN gateway type and its statistics are removed from the API"): the
// proto field is reserved, so the whole message is ignored and the proxy loads as
// an ordinary Dataplane — for a BUILTIN gateway, one with no inbounds. DELEGATED is
// gone too. Gateway tags outside the reserved prefixes move to Dataplane labels:
// UPGRADE.md requires it for MeshLoadBalancingStrategy localZone.affinityTags keys,
// which otherwise stop matching. The two labels are reserved and make a kuma-dp
// Dataplane fail to register on 3.0.
func fixDataplaneGatewayForV3(obj map[string]interface{}, name string) (bool, []string) {
	var warnings []string
	changed := false

	if net := dataplaneNetworking(obj); net != nil {
		if gwRaw, ok := net["gateway"]; ok {
			gw, _ := gwRaw.(map[string]interface{})
			gwType, _ := gw["type"].(string)
			if gwType == "" {
				gwType = "DELEGATED"
			}
			tags, _ := gw["tags"].(map[string]interface{})
			var res labelMerge
			if len(tags) > 0 {
				labels := resourceLabelMap(obj, true)
				res = mergeTagsIntoLabels(labels, tags)
				if len(labels) == 0 {
					deleteEmptyLabelMap(obj)
				}
			}
			delete(net, "gateway")
			changed = true

			msg := fmt.Sprintf("Dataplane %q: networking.gateway (type %s) is removed in 3.0 — auto-removed. "+
				"3.0 ignores the whole message and loads the proxy as an ordinary Dataplane.", name, gwType)
			if gwType == "BUILTIN" {
				msg += " The built-in gateway is gone (the BUILTIN ordinal is reserved), so this proxy " +
					"would have no inbounds: delete the Dataplane if it no longer serves traffic, or " +
					"replace it with a gateway you run."
			}
			if len(res.moved) > 0 {
				msg += " Moved gateway tags to Dataplane labels (MeshLoadBalancingStrategy " +
					"localZone.affinityTags keys only match labels now): " + strings.Join(res.moved, ", ") + "."
			}
			if len(res.reserved) > 0 {
				msg += " Dropped reserved-prefix tags that cannot be labels: " + strings.Join(res.reserved, ", ") + "."
			}
			if len(res.conflicts) > 0 {
				msg += " Not moved, an existing label has a different value: " + strings.Join(res.conflicts, ", ") + "."
			}
			msg += " Keep the gateway's listen ports out of inbound redirection: on Universal run kuma-dp " +
				"with --exclude-inbound-ports <ports> (or redirect.inbound.excludePorts in the transparent " +
				"proxy config); on Kubernetes annotate the Pod traffic.kuma.io/exclude-inbound-ports: " +
				"\"<ports>\" (comma-separated, no all-ports value) and the fronting Service kuma.io/ignore: \"true\"."
			warnings = append(warnings, msg)
		}
	}

	if labels := resourceLabelMap(obj, false); labels != nil {
		var removed []string
		for _, k := range []string{"kuma.io/gateway", "kuma.io/proxy-type"} {
			if v, ok := labels[k]; ok {
				removed = append(removed, fmt.Sprintf("%s=%v", k, v))
				delete(labels, k)
				changed = true
			}
		}
		if len(removed) > 0 {
			deleteEmptyLabelMap(obj)
			warnings = append(warnings, fmt.Sprintf(
				"Dataplane %q: label(s) %s auto-removed. 3.0 removes the gateway marking, and both keys sit "+
					"under the reserved kuma.io/ prefix, so a kuma-dp Dataplane still carrying them fails to "+
					"register. A targetRef or MeshLoadBalancingStrategy affinity key that selected on them "+
					"stops matching — move it to a label the control plane does not manage. Exclude the "+
					"gateway's listen ports with kuma-dp --exclude-inbound-ports.",
				name, strings.Join(removed, ", ")))
		}
	}
	return changed, warnings
}

// ---- tag-based outbounds → backendRef (3.0) ----------------------------------

// fixDataplaneOutboundTagsForV3 rewrites networking.outbound[] entries that use
// tags (no backendRef). 3.0 removes outbound tags: a write is rejected with
// "backendRef: must be defined", and a stored one loses its listener (UPGRADE.md
// "Dataplane outbounds must use backendRef").
//
// Only a Kubernetes-style kuma.io/service value (<name>_<ns>_svc_<port>) carries
// enough to build the backendRef — the MeshService port is not the outbound's
// local port. Anything else is reported with the snippet to write.
func fixDataplaneOutboundTagsForV3(obj map[string]interface{}, name string) (bool, []string) {
	net := dataplaneNetworking(obj)
	if net == nil {
		return false, nil
	}
	outs, _ := net["outbound"].([]interface{})
	changed := false
	var converted, manual, subsets, redundant []string
	for i, o := range outs {
		om, ok := o.(map[string]interface{})
		if !ok {
			continue
		}
		tags, hasTags := om["tags"].(map[string]interface{})
		if !hasTags {
			continue
		}
		port, _ := intValue(om["port"])
		where := fmt.Sprintf("outbound[%d] (port %d)", i, port)
		if _, hasRef := om["backendRef"]; hasRef {
			delete(om, "tags")
			changed = true
			redundant = append(redundant, where)
			continue
		}
		svc, _ := tags["kuma.io/service"].(string)
		var extra []string
		for _, k := range sortedMapKeys(tags) {
			if k != "kuma.io/service" {
				extra = append(extra, fmt.Sprintf("%s=%v", k, tags[k]))
			}
		}
		msName, msNS, msPort, ok := decodeServiceTagPort(svc)
		if !ok {
			msName = svc
			if msName == "" {
				msName = "<MeshService name>"
			}
			manual = append(manual, fmt.Sprintf(
				"%s: replace tags with `backendRef: {kind: MeshService, name: %s, port: <MeshService port>}`",
				where, msName))
			continue
		}
		om["backendRef"] = map[string]interface{}{
			"kind":   "MeshService",
			"labels": meshServiceRefLabels(msName, msNS),
			"port":   msPort,
		}
		delete(om, "tags")
		changed = true
		converted = append(converted, fmt.Sprintf("%s → MeshService %s/%s port %d", where, msNS, msName, msPort))
		if len(extra) > 0 {
			subsets = append(subsets, fmt.Sprintf("%s: %s", where, strings.Join(extra, ", ")))
		}
	}

	var warnings []string
	if len(converted) > 0 {
		msg := fmt.Sprintf("Dataplane %q: networking.outbound[].tags is removed in 3.0 (writes are rejected with "+
			"\"backendRef: must be defined\"). Auto-converted to backendRef: %s.", name, strings.Join(converted, "; "))
		if len(subsets) > 0 {
			msg += " The extra tags selected a subset that a backendRef cannot express and were dropped: " +
				strings.Join(subsets, "; ") + "."
		}
		warnings = append(warnings, msg)
	}
	if len(manual) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Dataplane %q: networking.outbound[].tags is removed in 3.0 — a stored outbound loses its listener "+
				"and new writes are rejected. Not converted, because the MeshService port is not in the "+
				"manifest: %s.", name, strings.Join(manual, "; ")))
	}
	if len(redundant) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Dataplane %q: removed tags from %s, which already set backendRef (3.0 drops outbound tags).",
			name, strings.Join(redundant, ", ")))
	}
	return changed, warnings
}

// ---- reachableServices → reachableBackends (3.0) ------------------------------

func dataplaneTransparentProxying(obj map[string]interface{}) map[string]interface{} {
	net := dataplaneNetworking(obj)
	tp, _ := net["transparentProxying"].(map[string]interface{})
	return tp
}

// dataplaneReachableBackendRefs returns transparentProxying.reachableBackends.refs.
func dataplaneReachableBackendRefs(obj map[string]interface{}) []interface{} {
	tp := dataplaneTransparentProxying(obj)
	rb, _ := tp["reachableBackends"].(map[string]interface{})
	refs, _ := rb["refs"].([]interface{})
	return refs
}

// convertReachableServices splits reachableServices entries into 3.0
// reachableBackends refs (decodable <name>_<ns>_svc_<port> values) and the
// entries it cannot convert.
func convertReachableServices(services []string) (refs []interface{}, rest []string) {
	for _, s := range services {
		n, ns, port, ok := decodeServiceTagPort(s)
		if !ok {
			rest = append(rest, s)
			continue
		}
		refs = append(refs, map[string]interface{}{
			"kind":   "MeshService",
			"labels": meshServiceRefLabels(n, ns),
			"port":   port,
		})
	}
	return refs, rest
}

// appendReachableRefs appends refs not already present (same kind, labels, port).
func appendReachableRefs(existing, add []interface{}) []interface{} {
	key := func(r interface{}) string {
		m, _ := r.(map[string]interface{})
		labels, _ := m["labels"].(map[string]interface{})
		var parts []string
		for _, k := range sortedMapKeys(labels) {
			parts = append(parts, k+"="+fmt.Sprint(labels[k]))
		}
		return fmt.Sprintf("%v|%s|%v|%v", m["kind"], strings.Join(parts, ","), m["name"], m["port"])
	}
	seen := map[string]bool{}
	for _, r := range existing {
		seen[key(r)] = true
	}
	for _, r := range add {
		if !seen[key(r)] {
			existing = append(existing, r)
			seen[key(r)] = true
		}
	}
	return existing
}

func describeRefs(refs []interface{}) string {
	var out []string
	for _, r := range refs {
		m, _ := r.(map[string]interface{})
		labels, _ := m["labels"].(map[string]interface{})
		out = append(out, fmt.Sprintf("%v %v/%v port %v", m["kind"], labels[labelK8sNamespace], labels[labelDisplayName], m["port"]))
	}
	return strings.Join(out, ", ")
}

// fixDataplaneReachableServicesForV3 converts transparentProxying.reachableServices
// into reachableBackends refs. 3.0 removes the field (proto reserved) together with
// the kuma.io/service-based mechanism (UPGRADE.md "reachableServices /
// kuma.io/transparent-proxying-reachable-services removed"). Entries that do not
// decode stay in place for warnDataplaneReachableServices to report.
func fixDataplaneReachableServicesForV3(obj map[string]interface{}, name string) (bool, []string) {
	tp := dataplaneTransparentProxying(obj)
	if tp == nil {
		return false, nil
	}
	raw, _ := tp["reachableServices"].([]interface{})
	if len(raw) == 0 {
		return false, nil
	}
	var services []string
	for _, s := range raw {
		services = append(services, fmt.Sprint(s))
	}
	refs, rest := convertReachableServices(services)
	if len(refs) == 0 {
		return false, nil
	}
	rb, _ := tp["reachableBackends"].(map[string]interface{})
	if rb == nil {
		rb = map[string]interface{}{}
		tp["reachableBackends"] = rb
	}
	existing, _ := rb["refs"].([]interface{})
	rb["refs"] = appendReachableRefs(existing, refs)
	if len(rest) == 0 {
		delete(tp, "reachableServices")
	} else {
		left := make([]interface{}, 0, len(rest))
		for _, s := range rest {
			left = append(left, s)
		}
		tp["reachableServices"] = left
	}
	return true, []string{fmt.Sprintf(
		"Dataplane %q: transparentProxying.reachableServices is removed in 3.0. Auto-converted to "+
			"reachableBackends.refs[]: %s.", name, describeRefs(refs))}
}

// ---- reachableBackends refs: name/namespace → labels (3.0) --------------------

// fixReachableBackendRefsForV3 rewrites refs[].name → labels[kuma.io/display-name]
// and refs[].namespace → labels[k8s.kuma.io/namespace] in place. 3.0 removes
// name/namespace from reachableBackends refs and from the kuma.io/reachable-backends
// annotation (UPGRADE.md "reachableBackends refs select backends by labels only").
//
// defaultNS fills k8s.kuma.io/namespace for a name-only ref when the owning
// resource's namespace is known: 2.14 resolved a name-only ref in the proxy's own
// namespace, while a display-name-only label matches every namespace.
func fixReachableBackendRefsForV3(refs []interface{}, defaultNS string) (bool, []string) {
	changed := false
	var notes []string
	for i, r := range refs {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		refName, _ := m["name"].(string)
		refNS, _ := m["namespace"].(string)
		_, hasName := m["name"]
		_, hasNS := m["namespace"]
		if !hasName && !hasNS {
			continue
		}
		labels, _ := m["labels"].(map[string]interface{})
		if labels == nil {
			labels = map[string]interface{}{}
		}
		if refName != "" {
			if prev, exists := labels[labelDisplayName]; exists && fmt.Sprint(prev) != refName {
				notes = append(notes, fmt.Sprintf("refs[%d]: labels[%s] is already %q, name %q not applied.",
					i, labelDisplayName, fmt.Sprint(prev), refName))
			} else {
				labels[labelDisplayName] = refName
			}
		}
		ns := refNS
		if ns == "" && refName != "" && defaultNS != "" {
			ns = defaultNS
			notes = append(notes, fmt.Sprintf("refs[%d] (%s) had no namespace; pinned to the workload's namespace %q, "+
				"which is where 2.14 resolved it.", i, refName, defaultNS))
		}
		if ns != "" {
			if _, exists := labels[labelK8sNamespace]; !exists {
				labels[labelK8sNamespace] = ns
			}
		}
		m["labels"] = labels
		delete(m, "name")
		delete(m, "namespace")
		changed = true
	}
	return changed, notes
}

// warnReachableBackendsNameRefsV2 is the v2 advisory for refs that still select by
// name/namespace. 2.14 already accepts the labels form, so the rewrite can be done
// before the upgrade; the migrator only performs it under --to-latest v3.
func warnReachableBackendsNameRefsV2(refs []interface{}, kind, name, where string) []string {
	n := 0
	for _, r := range refs {
		m, _ := r.(map[string]interface{})
		if _, ok := m["name"]; ok {
			n++
		} else if _, ok := m["namespace"]; ok {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s %q: %d ref(s) in %s select by name/namespace. 3.0 removes both — such refs resolve to nothing "+
			"and the proxy gets no outbounds. 2.14 already accepts the labels form: name → labels[%s], "+
			"namespace → labels[%s]. Re-run with --to-latest v3 to rewrite them.",
		kind, name, n, where, labelDisplayName, labelK8sNamespace)}
}

// ---- warn-only Dataplane removals (3.0) ---------------------------------------

// warnDataplaneV3Removals reports Dataplane fields 3.0 removes or deprecates that
// the migrator does not rewrite, because the replacement lives outside the manifest
// (kuma-dp flags, network reachability, CP config).
func warnDataplaneV3Removals(obj map[string]interface{}, name string, target TargetVersion) []string {
	if !target.IsV3() {
		return nil
	}
	var warnings []string
	net := dataplaneNetworking(obj)

	if net != nil {
		if addr, ok := net["advertisedAddress"]; ok {
			warnings = append(warnings, fmt.Sprintf(
				"Dataplane %q: networking.advertisedAddress (%v) is removed in 3.0 and dropped silently. Other "+
					"proxies, xDS endpoints and Envoy admin mTLS SANs fall back to networking.address (%v). "+
					"Make sure every proxy can reach this one on networking.address before upgrading.",
				name, addr, net["address"]))
		}
	}

	if tp := dataplaneTransparentProxying(obj); tp != nil {
		var deprecated []string
		for _, k := range []string{"redirectPortInbound", "redirectPortOutbound"} {
			if _, ok := tp[k]; ok {
				deprecated = append(deprecated, k)
			}
		}
		if len(deprecated) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"Dataplane %q: transparentProxying.%s still works on 3.0 but is removed in 3.1. Drop it and "+
					"start kuma-dp with --transparent-proxy, or --transparent-proxy-config <file> with "+
					"redirect.inbound.port / redirect.outbound.port for non-default ports.",
				name, strings.Join(deprecated, " / ")))
		}
		if _, ok := tp["ipFamilyMode"]; ok {
			warnings = append(warnings, fmt.Sprintf(
				"Dataplane %q: transparentProxying.ipFamilyMode is removed in 3.0 — the control plane uses the IP "+
					"family kuma-dp reports. For a non-default mode pass ipFamilyMode in a "+
					"--transparent-proxy-config file; on hosts with IPv6 disabled use ipFamilyMode: ipv4.",
				name))
		}
		if _, ok := tp["reachableBackends"]; !ok {
			warnings = append(warnings, fmt.Sprintf(
				"Dataplane %q: transparent proxying without reachableBackends. On 3.0 such a proxy gets no "+
					"generated outbounds and cannot reach any service, unless the control plane sets "+
					"defaults.restrictOutbound (KUMA_DEFAULTS_RESTRICT_OUTBOUND) to false. List the backends in "+
					"transparentProxying.reachableBackends.refs[] before upgrading.",
				name))
		}
	}

	probesSet := false
	if _, ok := obj["probes"]; ok {
		probesSet = true
	}
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		if _, ok := spec["probes"]; ok {
			probesSet = true
		}
	}
	if probesSet {
		warnings = append(warnings, fmt.Sprintf(
			"Dataplane %q: probes (virtual probes) is removed in 3.0 and dropped silently. On Kubernetes "+
				"Application Probe Proxy (kuma.io/application-probe-proxy-port) is the only probe-rewriting path; "+
				"on Universal, exclude the probe ports from inbound redirection if they must bypass mTLS.",
			name))
	}
	return warnings
}
