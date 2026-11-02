package migrator

import (
	"fmt"
	"sort"
	"strings"
)

// This file holds the 3.0 checks that are not tied to a single policy transform:
//
//   - the resource kinds 3.0 removes, which decide what the v3 pipeline keeps out
//     of the apply tree and lists under "Delete Before Upgrading to 3.0";
//   - Kong Mesh AccessRole / AccessAudit, whose type lists and qualifiers 3.0
//     narrows (UPGRADE_km.md, "Legacy policy types and qualifiers removed from
//     AccessRole and AccessAudit" and "AccessRole targetRef qualifiers restricted
//     to Mesh and Dataplane").

// ---- Kinds removed in 3.0 ---------------------------------------------------

// removedInV3SourceKinds are the resource kinds Kuma / Kong Mesh 3.0 no longer
// registers. Objects of these kinds must be deleted before the upgrade: the 3.0
// control plane rejects create/update for them and does not migrate stored ones.
//
//   - the 12 legacy policies (kuma UPGRADE.md "Legacy policy resources removed")
//   - ExternalService ("Legacy ExternalService resource removed")
//   - the built-in gateway: MeshGateway, MeshGatewayRoute, MeshGatewayInstance,
//     MeshGatewayConfig ("Built-in gateway API and CRDs removed")
//   - ZoneIngress / ZoneEgress ("ZoneIngress and ZoneEgress resources removed";
//     zone proxies are Dataplanes with zone proxy listeners)
//   - Kong Mesh OPAPolicy and MeshGlobalRateLimit (UPGRADE_km.md)
var removedInV3SourceKinds = map[string]bool{
	"TrafficPermission":   true,
	"TrafficRoute":        true,
	"TrafficLog":          true,
	"TrafficTrace":        true,
	"HealthCheck":         true,
	"CircuitBreaker":      true,
	"Retry":               true,
	"Timeout":             true,
	"RateLimit":           true,
	"FaultInjection":      true,
	"VirtualOutbound":     true,
	"ProxyTemplate":       true,
	"ExternalService":     true,
	"MeshGateway":         true,
	"MeshGatewayRoute":    true,
	"MeshGatewayInstance": true,
	"MeshGatewayConfig":   true,
	"ZoneIngress":         true,
	"ZoneEgress":          true,
	"OPAPolicy":           true,
	"MeshGlobalRateLimit": true,
}

// isRemovedInV3Kind reports whether kind is no longer a resource type in 3.0.
func isRemovedInV3Kind(kind string) bool {
	return removedInV3SourceKinds[kind]
}

// v3DeleteBeforeUpgradeNote returns the per-document --to-latest v3 advisory for a
// source object whose kind 3.0 removes, or "" when the kind is not removed or
// already carries its own delete guidance (MeshGateway/MeshGatewayRoute via
// removedGatewaySourceNote, MeshGatewayInstance via its conversion error,
// MeshGlobalRateLimit via warnMeshGlobalRateLimitRemoved).
func v3DeleteBeforeUpgradeNote(kind, name string, converted bool) string {
	switch kind {
	case "MeshGateway", "MeshGatewayRoute", "MeshGatewayInstance", "MeshGlobalRateLimit":
		return ""
	}
	if !isRemovedInV3Kind(kind) {
		return ""
	}
	if kind == "MeshGatewayConfig" {
		return fmt.Sprintf(
			"MeshGatewayConfig %q: removed in 3.0 with the rest of the built-in gateway API "+
				"(MeshGateway, MeshGatewayRoute, MeshGatewayInstance) and its CRD "+
				"meshgatewayconfigs.kuma.io. The 3.0 Helm upgrade deletes the CRD and every object "+
				"stored under it. Carry its settings (replicas, service type, pod and container "+
				"resources) over to the Deployment and Service of the gateway you run, then delete "+
				"it before upgrading. The original is kept under %s/ and is not part of any apply step.",
			name, ManualDir)
	}
	if converted {
		return fmt.Sprintf(
			"%s %q: %s is removed in 3.0. The 3.0 control plane rejects it and does not migrate "+
				"stored objects of this type. Delete it before upgrading, once its converted "+
				"replacement is in place (see \"Delete Before Upgrading to 3.0\" in the report).",
			kind, name, kind)
	}
	return fmt.Sprintf(
		"%s %q: %s is removed in 3.0 and was not converted. The original is kept under %s/ and is "+
			"not part of any apply step. Delete it before upgrading (see \"Delete Before Upgrading "+
			"to 3.0\" in the report).",
		kind, name, kind, ManualDir)
}

// kumactlDeleteType is the `kumactl delete <type>` spelling for the removed kinds.
// Legacy (proto) resources use kebab-case; the Mesh* resources use the lower-cased
// kind. Anything missing falls back to the lower-cased kind.
var kumactlDeleteType = map[string]string{
	"TrafficPermission":   "traffic-permission",
	"TrafficRoute":        "traffic-route",
	"TrafficLog":          "traffic-log",
	"TrafficTrace":        "traffic-trace",
	"HealthCheck":         "health-check",
	"CircuitBreaker":      "circuit-breaker",
	"Retry":               "retry",
	"Timeout":             "timeout",
	"RateLimit":           "rate-limit",
	"FaultInjection":      "fault-injection",
	"VirtualOutbound":     "virtual-outbound",
	"ProxyTemplate":       "proxytemplate",
	"ExternalService":     "external-service",
	"ZoneIngress":         "zone-ingress",
	"ZoneEgress":          "zone-egress",
	"OPAPolicy":           "opa-policy",
	"MeshGateway":         "meshgateway",
	"MeshGatewayRoute":    "meshgatewayroute",
	"MeshGlobalRateLimit": "meshglobalratelimit",
}

// globalScopedRemovedKinds are removed kinds that have no mesh association, so
// `kumactl delete` must not be given --mesh.
var globalScopedRemovedKinds = map[string]bool{
	"ZoneIngress":       true,
	"ZoneEgress":        true,
	"MeshGatewayConfig": true,
}

// kubernetesOnlyRemovedKinds are removed kinds that exist only as Kubernetes
// CRDs (no Universal resource type, nothing kumactl can address), so their
// delete command is always kubectl. MeshGatewayConfig is cluster-scoped.
var kubernetesOnlyRemovedKinds = map[string]bool{
	"MeshGatewayInstance": true,
	"MeshGatewayConfig":   true,
}

// ---- 3.0 resource type registry (for RBAC type lists) -----------------------

// kumaV3ResourceTypes is every resource type registered by Kuma 3.0 plus Kong
// Mesh 3.0, taken from the `Type model.ResourceType = "..."` declarations on
// kuma master and kong-mesh 3.0.1. AccessRole.rules[].types and
// AccessAudit.rules[].types are validated against this registry
// (registry.Global().NewObject); a name outside it is rejected.
var kumaV3ResourceTypes = map[string]bool{
	"AccessAudit": true, "AccessRole": true, "AccessRoleBinding": true,
	"Config": true, "Dataplane": true, "DataplaneInsight": true, "DataplaneOverview": true,
	"GlobalSecret": true, "HostnameGenerator": true, "Mesh": true,
	"MeshAccessLog": true, "MeshCircuitBreaker": true, "MeshExternalService": true,
	"MeshFaultInjection": true, "MeshHealthCheck": true, "MeshHTTPRoute": true,
	"MeshIdentity": true, "MeshInsight": true, "MeshLoadBalancingStrategy": true,
	"MeshMetric": true, "MeshMultiZoneService": true, "MeshOPA": true,
	"MeshOpenTelemetryBackend": true, "MeshOverview": true, "MeshPassthrough": true,
	"MeshProxyPatch": true, "MeshRateLimit": true, "MeshRetry": true, "MeshService": true,
	"MeshTCPRoute": true, "MeshTimeout": true, "MeshTLS": true, "MeshTrace": true,
	"MeshTrafficPermission": true, "MeshTrust": true, "MeshZoneAddress": true,
	"Secret": true, "Workload": true, "Zone": true, "ZoneInsight": true, "ZoneOverview": true,
}

// kumaV3PolicyTypes are the registered types whose spec is a targetRef policy
// (model.Policy). AccessRole rejects a rule whose types mix these with any other
// type ("cannot mix policies 2.0 with old policies"). Renaming a legacy policy to
// its Mesh* successor moves it from the "old" side to this side, so a rule that
// was valid on 2.x (e.g. [TrafficPermission, Dataplane]) can become invalid.
var kumaV3PolicyTypes = map[string]bool{
	"MeshAccessLog": true, "MeshCircuitBreaker": true, "MeshFaultInjection": true,
	"MeshHealthCheck": true, "MeshHTTPRoute": true, "MeshLoadBalancingStrategy": true,
	"MeshMetric": true, "MeshPassthrough": true, "MeshProxyPatch": true,
	"MeshRateLimit": true, "MeshRetry": true, "MeshTCPRoute": true, "MeshTimeout": true,
	"MeshTLS": true, "MeshTrace": true, "MeshTrafficPermission": true, "MeshOPA": true,
}

// rbacTypeSuccessors maps a type removed in 3.0 to its one-to-one successor in
// an RBAC type list. Granting (or auditing) the successor covers the objects the
// migrator converts the old type into.
var rbacTypeSuccessors = map[string]string{
	"Timeout":           "MeshTimeout",
	"CircuitBreaker":    "MeshCircuitBreaker",
	"Retry":             "MeshRetry",
	"HealthCheck":       "MeshHealthCheck",
	"TrafficLog":        "MeshAccessLog",
	"TrafficPermission": "MeshTrafficPermission",
	"FaultInjection":    "MeshFaultInjection",
	"RateLimit":         "MeshRateLimit",
	"TrafficTrace":      "MeshTrace",
	"ProxyTemplate":     "MeshProxyPatch",
	"ExternalService":   "MeshExternalService",
	"OPAPolicy":         "MeshOPA",
}

// rbacTypesWithoutSuccessor are types removed in 3.0 that have no single Kuma
// type to rename them to. They are dropped from the type list. The value says
// what to add by hand if the role or audit should keep covering that ground.
//
// The built-in gateway kinds have no successor in the registry: the Gateway API
// resources that replace them are Kubernetes objects, not Kuma types. ZoneIngress
// and ZoneEgress are not renamed to Dataplane, because that would widen the grant
// from zone proxies to every Dataplane.
var rbacTypesWithoutSuccessor = map[string]string{
	"TrafficRoute":        "add MeshHTTPRoute and/or MeshTCPRoute",
	"VirtualOutbound":     "add HostnameGenerator and MeshHTTPRoute/MeshTCPRoute",
	"MeshGateway":         "the Gateway API resources that replace it are not Kuma types",
	"MeshGatewayRoute":    "add MeshHTTPRoute/MeshTCPRoute if Kuma keeps doing the routing",
	"MeshGatewayInstance": "the delegated gateway that replaces it is not a Kuma type",
	"MeshGatewayConfig":   "the delegated gateway that replaces it is not a Kuma type",
	"ZoneIngress":         "zone proxies are Dataplanes now; add Dataplane only if the rule should cover every Dataplane",
	"ZoneEgress":          "zone proxies are Dataplanes now; add Dataplane only if the rule should cover every Dataplane",
	"ZoneIngressInsight":  "zone proxy health is in DataplaneInsight",
	"ZoneEgressInsight":   "zone proxy health is in DataplaneInsight",
	"ZoneIngressOverview": "zone proxy health is in DataplaneOverview",
	"ZoneEgressOverview":  "zone proxy health is in DataplaneOverview",
	"ServiceInsight":      "services are computed from MeshService now",
	"ServiceOverview":     "services are computed from MeshService now",
	"MeshGlobalRateLimit": "no replacement",
}

// rbacTypesRewrite is the result of rewriting one rules[].types list for 3.0.
type rbacTypesRewrite struct {
	out     []string    // rewritten, deduplicated list, original order kept
	renamed [][2]string // old → successor
	removed []string    // dropped: no successor
	unknown []string    // not a 3.0 type and not a known removal: left in place
	deduped bool        // the rename produced a duplicate that was collapsed
}

func (r rbacTypesRewrite) changed() bool {
	return len(r.renamed) > 0 || len(r.removed) > 0 || r.deduped
}

func rewriteRBACTypesForV3(types []string) rbacTypesRewrite {
	var r rbacTypesRewrite
	seen := map[string]bool{}
	for _, t := range types {
		nt := t
		if succ, ok := rbacTypeSuccessors[t]; ok {
			nt = succ
			r.renamed = append(r.renamed, [2]string{t, succ})
		} else if _, ok := rbacTypesWithoutSuccessor[t]; ok {
			r.removed = append(r.removed, t)
			continue
		} else if !kumaV3ResourceTypes[t] {
			r.unknown = append(r.unknown, t)
		}
		if seen[nt] {
			r.deduped = true
			continue
		}
		seen[nt] = true
		r.out = append(r.out, nt)
	}
	return r
}

func describeRenames(renamed [][2]string) string {
	parts := make([]string, 0, len(renamed))
	for _, p := range renamed {
		parts = append(parts, p[0]+"→"+p[1])
	}
	return strings.Join(parts, ", ")
}

func describeRemovals(removed []string) string {
	parts := make([]string, 0, len(removed))
	for _, t := range removed {
		parts = append(parts, fmt.Sprintf("%s (%s)", t, rbacTypesWithoutSuccessor[t]))
	}
	return strings.Join(parts, "; ")
}

// ---- AccessRole / AccessAudit shape helpers --------------------------------

// rbacRulesContainer returns the map that holds `rules`: spec on Kubernetes,
// the document root on Universal.
func rbacRulesContainer(obj map[string]interface{}) map[string]interface{} {
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		if _, has := spec["rules"]; has {
			return spec
		}
	}
	if _, has := obj["rules"]; has {
		return obj
	}
	return nil
}

func toStringList(v interface{}) []string {
	list, _ := v.([]interface{})
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toInterfaceList(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// rewriteRuleTypes applies the 3.0 type rewrite to one rule. Under v3 it
// returns drop=true when every listed type was removed: an empty `types` list
// means "every type", so leaving it empty would widen the rule.
func rewriteRuleTypes(rule map[string]interface{}, kind, name, path string, target TargetVersion) (changed, drop bool, warnings []string) {
	types := toStringList(rule["types"])
	if len(types) == 0 {
		return false, false, nil
	}
	rw := rewriteRBACTypesForV3(types)

	if !target.IsV3() {
		if len(rw.renamed) > 0 || len(rw.removed) > 0 {
			msg := fmt.Sprintf("%s %q: %s.types lists types removed in 3.0, where the role is rejected "+
				"(AccessRole: `unknown types`; AccessAudit: `invalid resource type`).", kind, name, path)
			if len(rw.renamed) > 0 {
				msg += " Rename on upgrade: " + describeRenames(rw.renamed) + "."
			}
			if len(rw.removed) > 0 {
				msg += " No successor, remove: " + describeRemovals(rw.removed) + "."
			}
			msg += " --to-latest v3 rewrites the list."
			warnings = append(warnings, msg)
		}
		return false, false, warnings
	}

	if len(rw.unknown) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%s %q: %s.types lists %s, which is not a 3.0 resource type. It was left in place; "+
				"3.0 rejects the rule until it is fixed.",
			kind, name, path, strings.Join(rw.unknown, ", ")))
	}
	if !rw.changed() {
		return false, false, warnings
	}
	if len(rw.out) == 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%s %q: %s was removed. Every type it listed is removed in 3.0 with no successor: %s. "+
				"An empty types list would cover every type, so the rule was dropped instead.",
			kind, name, path, describeRemovals(rw.removed)))
		return true, true, warnings
	}

	rule["types"] = toInterfaceList(rw.out)
	msg := fmt.Sprintf("%s %q: %s.types rewritten for 3.0.", kind, name, path)
	if len(rw.renamed) > 0 {
		msg += " Renamed: " + describeRenames(rw.renamed) + "."
	}
	if len(rw.removed) > 0 {
		msg += " Removed (no successor): " + describeRemovals(rw.removed) + "."
	}
	if rw.deduped {
		msg += " Duplicates collapsed."
	}
	if len(rw.renamed) > 0 && len(toStringList(rule["names"])) > 0 {
		msg += " The rule also lists names: converted policies keep their name unless they were " +
			"split (-0, -1, -outbound suffixes), so check the names still match."
	}
	warnings = append(warnings, msg)
	return true, false, warnings
}

// ---- AccessRole -------------------------------------------------------------

var rbacLegacyQualifierKeys = []string{"sources", "destinations", "selectors"}

// scanAccessRole checks a Kong Mesh AccessRole against the 3.0 changes:
//
//   - rules[].types: removed types renamed to their successor or dropped
//     (v3 rewrite; v2 advisory).
//   - rules[].types mixing targetRef policies with other types after the rename
//     (v3 warning: rejected with "cannot mix policies 2.0 with old policies").
//   - rules[].when[].targetRef.kind other than Mesh/Dataplane (warning only:
//     the label set that selects the same proxies is not in the manifest).
//   - targetRef.tags / targetRef.mesh on when[].targetRef, from.targetRef and
//     to.targetRef (warning: reserved in 3.0 and dropped silently).
//   - the removed sources/destinations/selectors qualifiers (see below).
//   - a role granting GENERATE_DATAPLANE_TOKEN without
//     VIEW_CONTROL_PLANE_METADATA, which loses read access to /config.
//
// Legacy qualifiers. `when` entries are ORed, and an entry that is not a
// targetRef/from/to/dpToken qualifier matches nothing. An empty `when` list
// matches everything. So, under v3:
//
//   - legacy keys next to other keys are stripped: 3.0 ignores them, so the
//     3.0 meaning of the entry does not change;
//   - entries holding only legacy keys (or `{}`) are removed when at least one
//     other entry remains in the same `when` list: they match nothing on 3.0, so
//     removing them does not change the grant;
//   - when every entry is legacy-only they are left in place. Removing them
//     would leave `when` empty, which grants access to every object of the
//     listed types. Leaving them makes 3.0 reject the role with
//     `qualifier cannot be empty`, which forces the rewrite.
func scanAccessRole(obj map[string]interface{}, name string, target TargetVersion) (bool, []string) {
	container := rbacRulesContainer(obj)
	if container == nil {
		return false, nil
	}
	rules, _ := container["rules"].([]interface{})
	if len(rules) == 0 {
		return false, nil
	}
	original := deepCopyValue(rules).([]interface{})

	var warnings []string
	changed := false
	var kept []interface{}
	dropped := 0
	grantsDPToken, grantsMetadata := false, false

	for i, r := range rules {
		rule, ok := r.(map[string]interface{})
		if !ok {
			kept = append(kept, r)
			continue
		}
		path := fmt.Sprintf("rules[%d]", i)

		for _, a := range toStringList(rule["access"]) {
			switch a {
			case "GENERATE_DATAPLANE_TOKEN":
				grantsDPToken = true
			case "VIEW_CONTROL_PLANE_METADATA":
				grantsMetadata = true
			}
		}

		c, drop, ws := rewriteRuleTypes(rule, "AccessRole", name, path, target)
		warnings = append(warnings, ws...)
		if drop {
			dropped++
			continue
		}
		changed = changed || c

		if target.IsV3() {
			warnings = append(warnings, warnRBACMixedTypes(rule, name, path)...)
		}

		c, ws = scanAccessRoleQualifiers(rule, name, path, target)
		warnings = append(warnings, ws...)
		changed = changed || c
		kept = append(kept, rule)
	}

	if dropped > 0 {
		if len(kept) == 0 {
			// Every rule would go. A role needs at least one rule, and on 3.0 this
			// one grants nothing anyway: keep it as extracted so the apply fails
			// loudly, and say to delete it.
			container["rules"] = original
			warnings = []string{fmt.Sprintf(
				"AccessRole %q: every rule lists only types removed in 3.0, so the role grants "+
					"nothing on 3.0. It was left unchanged and 3.0 rejects it with `unknown types`. "+
					"Delete the role and its AccessRoleBindings, or rewrite it for the Mesh* types.",
				name)}
			return false, warnings
		}
		container["rules"] = kept
		changed = true
	}

	if grantsDPToken && !grantsMetadata {
		warnings = append(warnings, fmt.Sprintf(
			"AccessRole %q grants GENERATE_DATAPLANE_TOKEN but not VIEW_CONTROL_PLANE_METADATA. "+
				"Before 3.0, /config was gated by GENERATE_DATAPLANE_TOKEN; on 3.0 it requires "+
				"VIEW_CONTROL_PLANE_METADATA, so a user relying on this role to read /config loses "+
				"that access. Add VIEW_CONTROL_PLANE_METADATA to the rule if that access is needed.",
			name))
	}

	return changed, warnings
}

// warnRBACMixedTypes flags a rule whose types mix targetRef policies with other
// types, which AccessRole rejects.
func warnRBACMixedTypes(rule map[string]interface{}, name, path string) []string {
	var policies, others []string
	for _, t := range toStringList(rule["types"]) {
		if kumaV3PolicyTypes[t] {
			policies = append(policies, t)
		} else {
			others = append(others, t)
		}
	}
	if len(policies) == 0 || len(others) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"AccessRole %q: %s.types mixes targetRef policies (%s) with other types (%s). 3.0 rejects "+
			"this with `cannot mix policies 2.0 with old policies`. Split it into two rules with the "+
			"same access, mesh and names; keep the when qualifiers on the policy rule only.",
		name, path, strings.Join(policies, ", "), strings.Join(others, ", "))}
}

// scanAccessRoleQualifiers handles rules[].when[] for scanAccessRole.
func scanAccessRoleQualifiers(rule map[string]interface{}, name, path string, target TargetVersion) (bool, []string) {
	when, _ := rule["when"].([]interface{})
	if len(when) == 0 {
		return false, nil
	}
	var warnings []string
	changed := false
	v3 := target.IsV3()

	var legacyOnly []int
	hasOther := false
	for j, w := range when {
		q, ok := w.(map[string]interface{})
		if !ok {
			hasOther = true
			continue
		}
		qpath := fmt.Sprintf("%s.when[%d]", path, j)

		var legacy, other []string
		for k, v := range q {
			if v == nil {
				continue
			}
			isLegacy := false
			for _, lk := range rbacLegacyQualifierKeys {
				if k == lk {
					isLegacy = true
					break
				}
			}
			if isLegacy {
				legacy = append(legacy, k)
			} else {
				other = append(other, k)
			}
		}
		sort.Strings(legacy)

		if len(other) == 0 {
			legacyOnly = append(legacyOnly, j)
		} else {
			hasOther = true
			if len(legacy) > 0 {
				if v3 {
					for _, k := range legacy {
						delete(q, k)
					}
					changed = true
					warnings = append(warnings, fmt.Sprintf(
						"AccessRole %q: %s.{%s} removed. 3.0 ignores these qualifiers next to other "+
							"fields, so the narrowing they expressed is gone on 3.0 either way. Express it "+
							"as a targetRef qualifier (kind: Dataplane with labels) if it is still needed.",
						name, qpath, strings.Join(legacy, ",")))
				} else {
					warnings = append(warnings, fmt.Sprintf(
						"AccessRole %q: %s.{%s} are removed in 3.0 and ignored next to other fields, so "+
							"the narrowing they express is lost on upgrade. Plan a targetRef qualifier "+
							"(kind: Dataplane with labels; labels on AccessRole qualifiers are accepted "+
							"from 3.0 only).",
						name, qpath, strings.Join(legacy, ",")))
				}
			}
		}

		warnings = append(warnings, warnRBACQualifierTargetRefs(q, name, qpath, target)...)
	}

	if len(legacyOnly) == 0 {
		return changed, warnings
	}

	idx := make([]string, len(legacyOnly))
	for i, j := range legacyOnly {
		idx[i] = fmt.Sprint(j)
	}
	entries := fmt.Sprintf("%s.when[%s]", path, strings.Join(idx, ","))

	switch {
	case !v3:
		warnings = append(warnings, fmt.Sprintf(
			"AccessRole %q: %s only set sources/destinations/selectors (or nothing). 3.0 removes "+
				"these qualifiers and rejects such an entry with `qualifier cannot be empty`. Rewrite "+
				"each as a targetRef qualifier (kind: Dataplane with labels selecting the same proxies) "+
				"as part of the upgrade; labels on AccessRole qualifiers are accepted from 3.0 only.",
			name, entries))
	case hasOther:
		keep := make([]interface{}, 0, len(when)-len(legacyOnly))
		drop := map[int]bool{}
		for _, j := range legacyOnly {
			drop[j] = true
		}
		for j, w := range when {
			if !drop[j] {
				keep = append(keep, w)
			}
		}
		rule["when"] = keep
		changed = true
		warnings = append(warnings, fmt.Sprintf(
			"AccessRole %q: %s removed. They only set sources/destinations/selectors (or nothing), "+
				"which 3.0 removes; such an entry matches nothing on 3.0 and is rejected with "+
				"`qualifier cannot be empty`. The other qualifiers of the rule are unchanged, so the "+
				"grant does not widen. If those entries granted access you still need, add a targetRef "+
				"qualifier (kind: Dataplane with labels).",
			name, entries))
	default:
		warnings = append(warnings, fmt.Sprintf(
			"AccessRole %q: %s only set sources/destinations/selectors (or nothing), which 3.0 "+
				"removes. They were left in place: removing them would leave `when` empty, which grants "+
				"access to every object of the listed types. 3.0 rejects the role with `qualifier cannot "+
				"be empty` until each entry is rewritten as a targetRef qualifier (kind: Dataplane with "+
				"labels selecting the same proxies).",
			name, entries))
	}
	return changed, warnings
}

// warnRBACQualifierTargetRefs checks the targetRef of one qualifier and of its
// from/to blocks.
func warnRBACQualifierTargetRefs(q map[string]interface{}, name, qpath string, target TargetVersion) []string {
	var warnings []string
	v3 := target.IsV3()

	check := func(ref map[string]interface{}, refPath string, topLevel bool) {
		if ref == nil {
			return
		}
		kind, _ := ref["kind"].(string)
		if topLevel && kind != "" && kind != "Mesh" && kind != "Dataplane" {
			hint := "usually as kind: Dataplane with labels that select the same proxies"
			if kind == "MeshSubset" {
				if tags, ok := ref["tags"].(map[string]interface{}); ok && len(tags) > 0 {
					hint = "as kind: Dataplane with its tags as labels if those keys exist as labels on the Dataplanes"
				}
			}
			if v3 {
				warnings = append(warnings, fmt.Sprintf(
					"AccessRole %q: %s.kind %s is rejected in 3.0 (`value '%s' is not supported`): a "+
						"qualifier's targetRef is matched against the policy's top-level targetRef, which "+
						"accepts only Mesh and Dataplane. Rewrite it, %s. Not converted: the label set is "+
						"not in the manifest.",
					name, refPath, kind, kind, hint))
			} else {
				warnings = append(warnings, fmt.Sprintf(
					"AccessRole %q: %s.kind %s is rejected in 3.0, which accepts only Mesh and "+
						"Dataplane here. Rewrite it, %s, as part of the upgrade (labels on AccessRole "+
						"qualifiers are accepted from 3.0 only).",
					name, refPath, kind, hint))
			}
		}
		var reserved []string
		for _, f := range []string{"mesh", "tags"} {
			if v, ok := ref[f]; ok && v != nil && v != "" {
				reserved = append(reserved, f)
			}
		}
		if len(reserved) > 0 {
			verb := "are reserved in 3.0 and dropped silently on load"
			if !v3 {
				verb = "are removed in 3.0 and dropped silently on load"
			}
			warnings = append(warnings, fmt.Sprintf(
				"AccessRole %q: %s.{%s} %s. A qualifier that relied on tags then matches more "+
					"policies, which widens the grant. Express the selection with kind: Dataplane and "+
					"labels instead.",
				name, refPath, strings.Join(reserved, ","), verb))
		}
	}

	if ref, ok := q["targetRef"].(map[string]interface{}); ok {
		check(ref, qpath+".targetRef", true)
	}
	for _, dir := range []string{"from", "to"} {
		if blk, ok := q[dir].(map[string]interface{}); ok {
			if ref, ok := blk["targetRef"].(map[string]interface{}); ok {
				check(ref, qpath+"."+dir+".targetRef", false)
			}
		}
	}
	return warnings
}

// ---- AccessAudit ------------------------------------------------------------

// scanAccessAudit applies the 3.0 type rewrite to every AccessAudit rule. An
// AccessAudit listing a removed type is rejected with `invalid resource type`.
// A rule whose types are all removed is dropped (an empty list would audit every
// type); if that leaves no rule, the document is left unchanged with a warning.
func scanAccessAudit(obj map[string]interface{}, name string, target TargetVersion) (bool, []string) {
	container := rbacRulesContainer(obj)
	if container == nil {
		return false, nil
	}
	rules, _ := container["rules"].([]interface{})
	if len(rules) == 0 {
		return false, nil
	}
	original := deepCopyValue(rules).([]interface{})

	var warnings []string
	changed := false
	var kept []interface{}
	dropped := 0
	for i, r := range rules {
		rule, ok := r.(map[string]interface{})
		if !ok {
			kept = append(kept, r)
			continue
		}
		c, drop, ws := rewriteRuleTypes(rule, "AccessAudit", name, fmt.Sprintf("rules[%d]", i), target)
		warnings = append(warnings, ws...)
		if drop {
			dropped++
			continue
		}
		changed = changed || c
		kept = append(kept, rule)
	}

	if dropped > 0 {
		if len(kept) == 0 {
			container["rules"] = original
			warnings = []string{fmt.Sprintf(
				"AccessAudit %q: every rule lists only types removed in 3.0. It was left unchanged and "+
					"3.0 rejects it with `invalid resource type`. Delete it, or rewrite it for the types "+
					"you want audited.", name)}
			return false, warnings
		}
		container["rules"] = kept
		changed = true
	}
	return changed, warnings
}
