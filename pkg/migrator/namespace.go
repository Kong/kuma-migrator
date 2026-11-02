package migrator

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// clusterScopedMeshKinds are the Kuma kinds starting with "Mesh" whose CRDs are
// cluster-scoped; every other Mesh* CRD is namespaced.
var clusterScopedMeshKinds = map[string]bool{
	"Mesh":              true,
	"MeshGateway":       true,
	"MeshGatewayConfig": true,
}

// namespacedConversionScenarios are the scenarios whose Kubernetes output is a
// namespaced Mesh* policy built from something that has no namespace to carry
// over: the legacy policies and OPAPolicy are cluster-scoped CRDs, and a Subset
// input may have been written without one.
var namespacedConversionScenarios = map[Scenario]bool{
	ScenarioLegacy:    true,
	ScenarioSubset:    true,
	ScenarioOPAPolicy: true,
}

// setMissingSystemNamespace puts every Kubernetes-format namespaced Mesh*
// document that has no metadata.namespace in the control-plane namespace
// (resolveSystemNamespace).
//
// Without it `kubectl apply` stores the policy in the caller's current
// namespace. There the Kuma webhooks do not run unless the namespace is labelled
// for injection, and the control plane computes kuma.io/policy-role from the
// placement (ComputePolicyRole in kuma pkg/core/resources/labels/compute.go): a
// policy outside the system namespace becomes workload-owner, consumer or
// producer and applies only to proxies of that namespace. In the system
// namespace the role is always system, which is the mesh-wide scope the source
// had. Universal documents have no namespace and are left alone.
func setMissingSystemNamespace(docs [][]byte, opts TransformOptions) ([][]byte, []string, error) {
	ns := resolveSystemNamespace(opts, "")
	var placed []string
	for i, doc := range docs {
		var obj map[string]interface{}
		if err := yaml.Unmarshal(doc, &obj); err != nil || obj == nil {
			continue
		}
		kind, _ := obj["kind"].(string)
		if av, _ := obj["apiVersion"].(string); !strings.HasPrefix(av, "kuma.io/") {
			continue
		}
		if !strings.HasPrefix(kind, "Mesh") || clusterScopedMeshKinds[kind] {
			continue
		}
		md, ok := obj["metadata"].(map[string]interface{})
		if !ok {
			md = map[string]interface{}{}
			obj["metadata"] = md
		}
		if existing, _ := md["namespace"].(string); existing != "" {
			continue
		}
		md["namespace"] = ns
		b, err := yaml.Marshal(obj)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal %s with namespace: %w", kind, err)
		}
		docs[i] = b
		placed = append(placed, fmt.Sprintf("%s %q", kind, extractNameFromObj(obj)))
	}
	if len(placed) == 0 || opts.SystemNamespace != "" {
		return docs, nil, nil
	}
	// The namespace is a guess only when the pre-pass found no evidence for it.
	return docs, []string{fmt.Sprintf(
		"%s: metadata.namespace set to %s, the Kuma default control-plane namespace — nothing in the "+
			"input named the namespace your control plane runs in. Change it (or set systemNamespace in "+
			"the config file) if the control plane is elsewhere, for example kong-mesh-system: a policy "+
			"outside the system namespace is scoped to its own namespace instead of the whole mesh.",
		strings.Join(placed, ", "), ns)}, nil
}
