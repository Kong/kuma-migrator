package migrator

import "sigs.k8s.io/yaml"

// needsApplyBeforeV3Upgrade reports whether a v3 output document must be applied
// on 2.x, before the Global control plane is upgraded: a MeshOPA or
// MeshExternalService that carries the 3.0 SecureDataSource shape.
//
// 3.0 rejects the flat DataSource shape and there is no KDS converter, so once
// the Global runs 3.0 a zone still on 2.x receives a document it cannot read in
// either shape unless it was rewritten first. Kong Mesh 2.14.6 accepts both
// shapes (Kong/kong-mesh#10968 for MeshOPA, kumahq/kuma#18867 for
// MeshExternalService), so the rewrite is applied there, on every control plane,
// before the Global upgrade. Applying the same file again after the upgrade is
// harmless.
func needsApplyBeforeV3Upgrade(doc []byte) bool {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(doc, &obj); err != nil {
		return false
	}
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	if kind != "MeshOPA" && kind != "MeshExternalService" {
		return false
	}
	return hasSecureDataSource(obj)
}

// hasSecureDataSource finds a typed data source anywhere in v: a map whose type
// is Secret with secretRef, or InsecureInline with insecureInline.
func hasSecureDataSource(v interface{}) bool {
	switch t := v.(type) {
	case map[string]interface{}:
		switch t["type"] {
		case "Secret":
			if _, ok := t["secretRef"]; ok {
				return true
			}
		case "InsecureInline":
			if _, ok := t["insecureInline"]; ok {
				return true
			}
		}
		for _, c := range t {
			if hasSecureDataSource(c) {
				return true
			}
		}
	case []interface{}:
		for _, c := range t {
			if hasSecureDataSource(c) {
				return true
			}
		}
	}
	return false
}
