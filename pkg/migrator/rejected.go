package migrator

import (
	"errors"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// RejectedDoc names one output document the target control plane rejects.
type RejectedDoc struct {
	// Index is the document's position in the docs slice returned alongside the error.
	Index   int
	Kind    string
	Name    string
	Reasons []string
}

// RejectedByTargetError is returned by TransformDocumentWithOptions when it emits
// one or more documents the selected target control plane rejects as they stand —
// most notably a MeshTrafficPermission that still carries spec.from under
// --to-latest v3.
//
// Unlike every other error from TransformDocumentWithOptions, it comes WITH the
// converted output: docs and warnings are populated exactly as on success. The
// caller should write the documents listed in Rejected to the manual tree
// (ManualDir) instead of the apply tree, write the others normally, and report
// the reasons. A caller that treats it as an ordinary error and preserves the
// original input in the manual tree is also correct, only less helpful.
//
// Detect it with errors.As or AsRejectedByTarget.
type RejectedByTargetError struct {
	Target   TargetVersion
	Rejected []RejectedDoc
}

func (e *RejectedByTargetError) Error() string {
	parts := make([]string, 0, len(e.Rejected))
	for _, r := range e.Rejected {
		parts = append(parts, fmt.Sprintf("%s %q: %s", r.Kind, r.Name, strings.Join(r.Reasons, "; ")))
	}
	return fmt.Sprintf("rejected by the %s control plane as converted (written for manual follow-up, "+
		"not for apply): %s", e.Target.Describe(), strings.Join(parts, " | "))
}

// IsRejected reports whether the document at index i is one the target rejects.
func (e *RejectedByTargetError) IsRejected(i int) bool {
	for _, r := range e.Rejected {
		if r.Index == i {
			return true
		}
	}
	return false
}

// AsRejectedByTarget unwraps err into a *RejectedByTargetError.
func AsRejectedByTarget(err error) (*RejectedByTargetError, bool) {
	var r *RejectedByTargetError
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// rejectedByTarget inspects the final output documents and returns a
// *RejectedByTargetError naming every one the target rejects, or nil.
// Only --to-latest v3 output is checked: v2 output is applied back to the 2.x
// control plane it was extracted from.
func rejectedByTarget(docs [][]byte, target TargetVersion) error {
	if !target.IsV3() {
		return nil
	}
	var rejected []RejectedDoc
	for i, doc := range docs {
		var obj map[string]interface{}
		if err := yaml.Unmarshal(doc, &obj); err != nil || obj == nil {
			continue
		}
		kind, _ := obj["kind"].(string)
		if kind == "" {
			kind, _ = obj["type"].(string)
		}
		if !isKumaResource(obj) {
			continue
		}
		name := extractNameFromObj(obj)
		if reasons := v3RejectionReasons(obj, name, kind); len(reasons) > 0 {
			rejected = append(rejected, RejectedDoc{Index: i, Kind: kind, Name: name, Reasons: reasons})
		}
	}
	if len(rejected) == 0 {
		return nil
	}
	return &RejectedByTargetError{Target: target, Rejected: rejected}
}

// v3TopLevelChecked reports whether kind is a policy whose 3.0 validator accepts
// only kind: Mesh or kind: Dataplane in spec.targetRef.
func v3TopLevelChecked(kind string) bool {
	_, known := v3ToTargetRefKinds[kind]
	return known || kind == "MeshOPA"
}

// v3RejectionReasons lists why a Kuma 3.0 control plane rejects obj as it
// stands. Every reason corresponds to a validator or schema rule on kuma master:
//   - spec.from on MeshTrafficPermission / MeshFaultInjection ("spec.from:
//     unknown field"); 3.0 has no mechanical rewrite for a client selector.
//   - a top-level targetRef kind other than Mesh or Dataplane.
//   - name/namespace on a Dataplane, MeshService, MeshExternalService,
//     MeshMultiZoneService or MeshHTTPRoute ref (labels only on 3.0).
//   - a spec.to[] targetRef kind the policy's 3.0 validator does not list
//     (warnV3ToTargetRefKinds).
func v3RejectionReasons(obj map[string]interface{}, name, kind string) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	var reasons []string

	if meshSubsetFromKinds[kind] {
		if from, ok := spec["from"].([]interface{}); ok && len(from) > 0 {
			reasons = append(reasons, fmt.Sprintf(
				"spec.from is removed from %s in Kuma 3.0 (rejected as \"spec.from: unknown field\"); "+
					"express each client as a rules[] SPIFFE ID/SNI match by hand", kind))
		}
	}

	if !v3TopLevelChecked(kind) {
		return reasons
	}

	type refAt struct {
		path string
		ref  map[string]interface{}
	}
	var refs []refAt
	if tr, ok := spec["targetRef"].(map[string]interface{}); ok {
		refs = append(refs, refAt{"spec.targetRef", tr})
		if k, _ := tr["kind"].(string); k != "" && k != "Mesh" && k != "Dataplane" {
			reasons = append(reasons, fmt.Sprintf(
				"spec.targetRef.kind %s is not accepted: Mesh and Dataplane are the only top-level kinds", k))
		}
	}
	to, _ := spec["to"].([]interface{})
	for i, entry := range to {
		if em, ok := entry.(map[string]interface{}); ok {
			if tr, ok := em["targetRef"].(map[string]interface{}); ok {
				refs = append(refs, refAt{fmt.Sprintf("spec.to[%d].targetRef", i), tr})
			}
		}
	}
	for _, r := range refs {
		k, _ := r.ref["kind"].(string)
		if k != "Dataplane" && !labelSelectedKinds[k] {
			continue
		}
		_, hasName := r.ref["name"]
		_, hasNS := r.ref["namespace"]
		if hasName || hasNS {
			reasons = append(reasons, fmt.Sprintf(
				"%s selects a %s by name/namespace; 3.0 selects it by labels only", r.path, k))
		}
	}
	reasons = append(reasons, warnV3ToTargetRefKinds(obj, name, kind)...)
	return reasons
}

// unnamedOutputError returns an error when a generated document has no kind/type
// or no name. Such a document is unusable — it would be written as
// other/unknown.yaml and fail on apply — and always means a conversion dropped
// the resource's identity, so it must never be written silently.
func unnamedOutputError(doc []byte, i int) error {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(doc, &obj); err != nil {
		return fmt.Errorf("output document %d is not valid YAML: %w", i, err)
	}
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	name := ""
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		name, _ = md["name"].(string)
	}
	if name == "" {
		name, _ = obj["name"].(string)
	}
	if kind == "" || name == "" {
		return fmt.Errorf("internal error: output document %d lost its identity (kind/type %q, name %q); "+
			"refusing to write it — the original is preserved instead", i, kind, name)
	}
	return nil
}
