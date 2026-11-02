package migrator

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// Kuma 3.0 changes that apply to MeshExternalService, both to documents that
// arrive as MeshExternalService (passthrough) and to the MeshExternalService
// that ScenarioExternalService converts an ExternalService into. All of them
// run from the post-pass on every output document.

const zoneLabel = "kuma.io/zone"

// mesTLSVerification returns spec.tls.verification of a
// MeshExternalService (or the top-level tls block some Universal dumps carry).
func mesTLSVerification(obj map[string]interface{}) map[string]interface{} {
	tls, _ := obj["tls"].(map[string]interface{})
	if tls == nil {
		if spec, ok := obj["spec"].(map[string]interface{}); ok {
			tls, _ = spec["tls"].(map[string]interface{})
		}
	}
	if tls == nil {
		return nil
	}
	verification, _ := tls["verification"].(map[string]interface{})
	return verification
}

// mesDataSourceFields are the TLS verification fields 3.0 retypes from DataSource
// to SecureDataSource.
var mesDataSourceFields = []string{"caCert", "clientCert", "clientKey"}

// fixMESDataSourcesForV3 rewrites the legacy flat DataSource on
// spec.tls.verification.{caCert,clientCert,clientKey} to 3.0's SecureDataSource
// (UPGRADE.md, "MeshExternalService TLS verification uses the SecureDataSource
// shape"; types in kuma api/common/v1alpha1/datasource):
//
//	inlineString: <text>  → type: InsecureInline, insecureInline: {value: <text>}
//	secret: <name>        → type: Secret, secretRef: {kind: Secret, name: <name>}
//
// `inline` (base64) is left alone and still warned about by
// warnMeshExternalServiceDataSource: converting it means decoding credential
// material and writing it out in plain text, which is the operator's call.
// A data source that already has a `type` discriminator is not touched — 2.14.6
// rejects a source that mixes both shapes, so there is nothing safe to merge.
func fixMESDataSourcesForV3(obj map[string]interface{}, name string) (bool, []string) {
	verification := mesTLSVerification(obj)
	if verification == nil {
		return false, nil
	}

	changed := false
	var warnings []string
	for _, field := range mesDataSourceFields {
		ds, _ := verification[field].(map[string]interface{})
		if ds == nil {
			continue
		}
		if _, typed := ds["type"]; typed {
			continue
		}
		if _, hasInline := ds["inline"]; hasInline {
			continue // warn-only, see above
		}
		var replacement map[string]interface{}
		var from string
		if v, ok := ds["inlineString"].(string); ok && len(ds) == 1 {
			replacement = map[string]interface{}{
				"type":           "InsecureInline",
				"insecureInline": map[string]interface{}{"value": v},
			}
			from = "inlineString"
		} else if v, ok := ds["secret"].(string); ok && len(ds) == 1 {
			replacement = map[string]interface{}{
				"type":      "Secret",
				"secretRef": map[string]interface{}{"kind": "Secret", "name": v},
			}
			from = "secret"
		} else {
			continue
		}
		verification[field] = replacement
		changed = true
		warnings = append(warnings, fmt.Sprintf(
			"MeshExternalService %q: spec.tls.verification.%s.%s rewritten to the SecureDataSource shape "+
				"(type: %s) that Kuma 3.0 requires. Apply it while every control plane runs 2.14.6 or later and "+
				"before upgrading the global control plane to 3.0: 2.14.6 is the first release that accepts the "+
				"new shape, and a zone older than that drops the new fields and falls back to the system CA.",
			name, field, from, replacement["type"]))
	}
	return changed, warnings
}

// warnMESZoneLabel flags a MeshExternalService labelled kuma.io/zone under v3.
//
// On 2.x that label restricted the service to its zone: other zones reached it
// through that zone's ingress and egress. 3.0 removes per-zone MES routing
// (UPGRADE.md, "Per-zone MeshExternalService routing removed") — every zone's
// local egress now dials the endpoint directly. The label is also set by the
// control plane on a MeshExternalService created on a zone CP, so a zone extract
// carries it without anyone having written it.
func warnMESZoneLabel(obj map[string]interface{}, name string, target TargetVersion) []string {
	if !target.IsV3() {
		return nil
	}
	zone := objLabels(obj)[zoneLabel]
	if zone == "" {
		return nil
	}
	return []string{fmt.Sprintf(
		"MeshExternalService %q: labelled kuma.io/zone=%s. Kuma 3.0 removes per-zone MeshExternalService "+
			"routing: the label no longer restricts the service to zone %s, and traffic from other zones no "+
			"longer goes through that zone's egress. Every zone's local egress now dials the endpoint directly, "+
			"so check that each zone has a network path to it before upgrading.",
		name, zone, zone)}
}

// MeshIdentityIndex records the meshes that have at least one MeshIdentity in
// the input set.
type MeshIdentityIndex map[string]bool

// parseMeshIdentityMesh returns the mesh of a MeshIdentity document, or "" for
// any other document.
func parseMeshIdentityMesh(raw []byte) string {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	if objKind(obj) != "MeshIdentity" {
		return ""
	}
	return objMesh(obj)
}

// warnMESRequiresMeshIdentity flags, under v3, a MeshExternalService whose mesh
// has no MeshIdentity in the input set.
//
// 3.0 only generates a MeshExternalService cluster for a client proxy that has a
// workload identity (UPGRADE.md, "MeshExternalService clusters require a
// MeshIdentity"). Without one the outbound listener is still generated, so the
// request fails locally with a 503 cluster_not_found instead of reaching the
// egress. The index cannot tell whether the MeshIdentity's selector covers the
// clients, only that one exists; and a partial extract may not contain it, so
// the warning says what to check rather than asserting it is missing on the CP.
func warnMESRequiresMeshIdentity(doc []byte, opts TransformOptions) []string {
	if !opts.Target.IsV3() {
		return nil
	}
	var obj map[string]interface{}
	if err := yaml.Unmarshal(doc, &obj); err != nil {
		return nil
	}
	if objKind(obj) != "MeshExternalService" {
		return nil
	}
	mesh := objMesh(obj)
	if opts.MeshIdentities[mesh] {
		return nil
	}
	return []string{fmt.Sprintf(
		"MeshExternalService %q (mesh %s): no MeshIdentity for mesh %s in the input set. Kuma 3.0 gives a "+
			"client proxy a MeshExternalService cluster only when it has a workload identity — without a "+
			"MeshIdentity matching the client proxies, requests to this service fail in the client's Envoy "+
			"with 503 cluster_not_found. Create a MeshIdentity covering every client of this service before "+
			"upgrading (skip this if one exists on the control plane but was not extracted).",
		extractNameFromObj(obj), mesh, mesh)}
}

// ---- small map helpers ------------------------------------------------------

// objKind returns kind, falling back to Universal's type.
func objKind(obj map[string]interface{}) string {
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	return kind
}

// objLabels returns metadata.labels (Kubernetes) merged over top-level labels
// (Universal).
func objLabels(obj map[string]interface{}) map[string]string {
	out := map[string]string{}
	collect := func(v interface{}) {
		m, _ := v.(map[string]interface{})
		for k, val := range m {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
	}
	collect(obj["labels"])
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		collect(meta["labels"])
	}
	return out
}

// objMesh returns the mesh a resource belongs to: the Universal mesh field, then
// the kuma.io/mesh label, then "default".
func objMesh(obj map[string]interface{}) string {
	if m, ok := obj["mesh"].(string); ok && m != "" {
		return m
	}
	if m := objLabels(obj)[meshLabel]; m != "" {
		return m
	}
	return "default"
}
