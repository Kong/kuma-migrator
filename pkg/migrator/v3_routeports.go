package migrator

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Kuma 3.0, UPGRADE.md "A MeshHTTPRoute backendRef naming a port the destination
// lacks now fails closed": a backendRef whose MeshService exists but has no
// matching port no longer falls through to another port of that MeshService. It
// counts as unresolved, and a rule whose backendRefs are all unresolved answers
// 500.
//
// The port lookup is MeshServiceResource.FindPortByName, which compares the
// reference with each spec.ports[] entry's name and with its port number written
// as a string. backendRefs[].port (a number) and backendRefs[].sectionName (a
// string) both go through it, so either matches a port by name or by number.
// targetPort is never compared.

// meshServiceKey identifies a MeshService the way a backendRef selects it.
type meshServiceKey struct {
	mesh      string
	namespace string // "" on Universal
	name      string // display name
}

// meshServicePort is one spec.ports[] entry.
type meshServicePort struct {
	name string
	port int
}

// MeshServicePortIndex records the ports of every MeshService in the input. It
// is built by BuildTransformOptions and shared by pointer across the run, so the
// "not in the input" note is emitted once per run rather than once per ref.
type MeshServicePortIndex struct {
	services     map[meshServiceKey][]meshServicePort
	notedMissing bool
}

func newMeshServicePortIndex() *MeshServicePortIndex {
	return &MeshServicePortIndex{services: map[meshServiceKey][]meshServicePort{}}
}

// add indexes raw when it is a MeshService (either layout).
func (ix *MeshServicePortIndex) add(raw []byte) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil || obj == nil {
		return
	}
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	if kind != "MeshService" {
		return
	}
	labels := stringMap(objLabels(obj))
	key := meshServiceKey{mesh: docMesh(obj), name: labels[displayNameLabel]}
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		key.namespace, _ = md["namespace"].(string)
		if key.name == "" {
			key.name, _ = md["name"].(string)
		}
	}
	if key.namespace == "" {
		key.namespace = labels[kubeNamespaceLabel]
	}
	if key.name == "" {
		key.name, _ = obj["name"].(string)
	}
	if key.name == "" {
		return
	}
	spec, _ := obj["spec"].(map[string]interface{})
	var ports []meshServicePort
	for _, p := range asSlice(spec["ports"]) {
		pm, _ := p.(map[string]interface{})
		if pm == nil {
			continue
		}
		mp := meshServicePort{}
		mp.name, _ = pm["name"].(string)
		mp.port, _ = intValue(pm["port"])
		ports = append(ports, mp)
	}
	ix.services[key] = append(ix.services[key], ports...)
}

// lookup returns the port lists of the MeshServices a ref selects. An empty
// namespace matches any namespace (Universal, or a label ref without one).
func (ix *MeshServicePortIndex) lookup(mesh, namespace, name string) [][]meshServicePort {
	var out [][]meshServicePort
	for k, ports := range ix.services {
		if k.mesh != mesh || k.name != name {
			continue
		}
		if namespace != "" && k.namespace != "" && k.namespace != namespace {
			continue
		}
		out = append(out, ports)
	}
	return out
}

// docMesh returns the mesh of a document: the Universal top-level field, else
// the kuma.io/mesh label, else "default".
func docMesh(obj map[string]interface{}) string {
	if m, ok := obj["mesh"].(string); ok && m != "" {
		return m
	}
	if m, ok := objLabels(obj)[meshLabel].(string); ok && m != "" {
		return m
	}
	return "default"
}

// portMatches mirrors FindPortByName: ref matches a port by name or by its
// number written as a string.
func portMatches(ports []meshServicePort, ref string) bool {
	for _, p := range ports {
		if p.name == ref || strconv.Itoa(p.port) == ref {
			return true
		}
	}
	return false
}

func describePorts(ports []meshServicePort) string {
	if len(ports) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		s := strconv.Itoa(p.port)
		if p.name != "" {
			s += " (" + p.name + ")"
		}
		parts = append(parts, s)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// warnRouteBackendRefPortsForV3 checks every MeshHTTPRoute
// spec.to[].rules[].default.backendRefs[] entry of kind MeshService that names
// a port (port or sectionName) against the MeshServices in the input. v3 only;
// a nil index (no pre-pass) checks nothing. A MeshService that is not in the
// input cannot be checked; that is said once per run.
func warnRouteBackendRefPortsForV3(doc []byte, opts TransformOptions) []string {
	ix := opts.MeshServicePorts
	if !opts.Target.IsV3() || ix == nil {
		return nil
	}
	var obj map[string]interface{}
	if err := yaml.Unmarshal(doc, &obj); err != nil || obj == nil {
		return nil
	}
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	if kind != "MeshHTTPRoute" {
		return nil
	}
	name := extractNameFromObj(obj)
	policyMesh := docMesh(obj)
	policyNS := ""
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		policyNS, _ = md["namespace"].(string)
	}
	spec, _ := obj["spec"].(map[string]interface{})

	var warnings []string
	for i, entry := range asSlice(spec["to"]) {
		em, _ := entry.(map[string]interface{})
		if em == nil {
			continue
		}
		collectBackendRefs(em["rules"], fmt.Sprintf("spec.to[%d].rules", i), func(path string, br map[string]interface{}) {
			// Mirrors do not take part in the split the UPGRADE note is about.
			if strings.Contains(path, "requestMirror") {
				return
			}
			if k, _ := br["kind"].(string); k != "MeshService" {
				return
			}
			var refPort, field string
			if p, ok := intValue(br["port"]); ok && br["port"] != nil {
				refPort, field = strconv.Itoa(p), "port"
			} else if s, _ := br["sectionName"].(string); s != "" {
				refPort, field = s, "sectionName"
			} else {
				return
			}

			labels := stringMap(br["labels"])
			svc, ns, mesh := labels[displayNameLabel], labels[kubeNamespaceLabel], labels[meshLabel]
			if svc == "" {
				svc, _ = br["name"].(string)
				if svc == "" {
					return // selected by arbitrary labels: nothing to look up
				}
				if ns == "" {
					ns, _ = br["namespace"].(string)
				}
				if ns == "" {
					ns = policyNS // a name-only 2.x ref resolves in the route's namespace
				}
			}
			if mesh == "" {
				mesh, _ = br["mesh"].(string)
			}
			if mesh == "" {
				mesh = policyMesh
			}

			candidates := ix.lookup(mesh, ns, svc)
			if len(candidates) == 0 {
				if !ix.notedMissing {
					ix.notedMissing = true
					warnings = append(warnings, fmt.Sprintf(
						"MeshHTTPRoute %q: %s names MeshService %q, which is not in the input, so its %s %s "+
							"could not be checked. Kuma 3.0 no longer lets a backendRef naming a port the "+
							"destination lacks fall through to another port: the backendRef is unresolved, and a "+
							"rule whose backendRefs are all unresolved answers 500. Check by hand every route "+
							"backendRef whose MeshService was not extracted (this note is printed once per run).",
						name, path, svc, field, refPort))
				}
				return
			}
			for _, ports := range candidates {
				if portMatches(ports, refPort) {
					return
				}
			}
			where := fmt.Sprintf("mesh %q", mesh)
			if ns != "" {
				where += fmt.Sprintf(", namespace %q", ns)
			}
			warnings = append(warnings, fmt.Sprintf(
				"MeshHTTPRoute %q: %s.%s is %s, which MeshService %q (%s) does not have — its ports are %s "+
					"(matched by name or port number, never targetPort). On 2.x the backendRef fell through to "+
					"another port of that MeshService; Kuma 3.0 counts it as unresolved, and a rule whose "+
					"backendRefs are all unresolved answers 500. Fix the %s before upgrading.",
				name, path, field, refPort, svc, where, describePorts(candidates[0]), field))
		})
	}
	return warnings
}
