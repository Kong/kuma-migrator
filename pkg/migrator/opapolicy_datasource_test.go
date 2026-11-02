package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const opaPolicyInlineBase64 = `apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
mesh: default
metadata:
  name: opa-b64
spec:
  selectors:
  - match:
      kuma.io/service: '*'
  conf:
    agentConfig:
      inline: ZGVjaXNpb25fbG9nczogdHJ1ZQo=
    policies:
    - inline: cGFja2FnZSBlbnZveS5hdXRoegpkZWZhdWx0IGFsbG93ID0gdHJ1ZQo=
    - inlineString: "package x"
    - secret: opa-rego
    - file: /tmp/x.rego
`

func opaDefault(t *testing.T, doc []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := yaml.Unmarshal(doc, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec, _ := out["spec"].(map[string]interface{})
	def, _ := spec["default"].(map[string]interface{})
	return def
}

// A base64 conf.policies[].inline used to become rego: {} with no warning.
func TestTransformOPAPolicy_InlineBase64_V2(t *testing.T) {
	docs, warnings, err := TransformOPAPolicy([]byte(opaPolicyInlineBase64), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := opaDefault(t, docs[0])
	pols, _ := def["appendPolicies"].([]interface{})
	if len(pols) != 3 {
		t.Fatalf("want 3 appendPolicies (the file: entry is not migratable), got %d: %v", len(pols), pols)
	}
	wantKeys := []string{"inline", "inlineString", "secret"}
	for i, p := range pols {
		rego, _ := p.(map[string]interface{})["rego"].(map[string]interface{})
		if len(rego) == 0 {
			t.Errorf("appendPolicies[%d].rego is empty", i)
		}
		if _, ok := rego[wantKeys[i]]; !ok {
			t.Errorf("appendPolicies[%d].rego = %v, want key %s", i, rego, wantKeys[i])
		}
	}
	rego0, _ := pols[0].(map[string]interface{})["rego"].(map[string]interface{})
	if rego0["inline"] != "cGFja2FnZSBlbnZveS5hdXRoegpkZWZhdWx0IGFsbG93ID0gdHJ1ZQo=" {
		t.Errorf("inline must be copied unchanged under v2, got %v", rego0)
	}
	ac, _ := def["agentConfig"].(map[string]interface{})
	if ac["inline"] != "ZGVjaXNpb25fbG9nczogdHJ1ZQo=" {
		t.Errorf("agentConfig.inline must be copied, got %v", ac)
	}
	if warningContaining(warnings, "conf.policies[3]", "file", "NOT migrated") == "" {
		t.Errorf("expected a warning for the unrecognised source, got: %v", warnings)
	}
}

// Under v3 the copied inline is decoded into the SecureDataSource shape.
func TestTransformOPAPolicy_InlineBase64_V3(t *testing.T) {
	docs, _, err := TransformOPAPolicy([]byte(opaPolicyInlineBase64), TargetV3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := opaDefault(t, docs[0])
	pols, _ := def["appendPolicies"].([]interface{})
	rego, _ := pols[0].(map[string]interface{})["rego"].(map[string]interface{})
	if rego["type"] != "InsecureInline" {
		t.Fatalf("rego[0] = %v, want type InsecureInline", rego)
	}
	ii, _ := rego["insecureInline"].(map[string]interface{})
	if v, _ := ii["value"].(string); !strings.Contains(v, "package envoy.authz") {
		t.Errorf("rego[0] value not decoded: %q", v)
	}
	ac, _ := def["agentConfig"].(map[string]interface{})
	aci, _ := ac["insecureInline"].(map[string]interface{})
	if v, _ := aci["value"].(string); v != "decision_logs: true\n" {
		t.Errorf("agentConfig not decoded: %v", ac)
	}
}

func TestTransformOPAPolicy_UnrecognisedSourcesNeverEmpty(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: opa-bad
spec:
  targetRef:
    kind: Mesh
  conf:
    agentConfig:
      file: /etc/opa.yaml
    policies:
    - {}
    - "package x"
`
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		docs, warnings, err := TransformOPAPolicy([]byte(input), target)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", target, err)
		}
		def := opaDefault(t, docs[0])
		if _, ok := def["appendPolicies"]; ok {
			t.Errorf("%s: no appendPolicies expected, got %v", target, def["appendPolicies"])
		}
		if _, ok := def["agentConfig"]; ok {
			t.Errorf("%s: no agentConfig expected, got %v", target, def["agentConfig"])
		}
		for _, f := range []string{"conf.agentConfig", "conf.policies[0]", "conf.policies[1]"} {
			if warningContaining(warnings, f, "NOT migrated") == "" {
				t.Errorf("%s: expected a NOT migrated warning for %s, got: %v", target, f, warnings)
			}
		}
	}
}
