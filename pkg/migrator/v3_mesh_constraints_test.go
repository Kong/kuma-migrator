package migrator

import (
	"strings"
	"testing"
)

const meshWithConstraints = `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  constraints:
    dataplaneProxy:
      requirements:
        - tags:
            kuma.io/zone: east
      restrictions:
        - tags:
            env: dev
`

func TestMeshConstraints_V3RemovedWithWarning(t *testing.T) {
	docs, warnings := transformAll(t, meshWithConstraints, TargetV3)
	if len(docs) == 0 {
		t.Fatal("no output")
	}
	spec, _ := docs[0]["spec"].(map[string]interface{})
	if _, ok := spec["constraints"]; ok {
		t.Errorf("constraints must be removed under v3: %v", spec)
	}
	if !hasWarning(warnings, `constraints.dataplaneProxy (1 requirements, 1 restrictions) is removed in 3.0 and has been removed from the output`) {
		t.Errorf("missing v3 constraints warning: %v", warnings)
	}
	if !hasWarning(warnings, "kumactl generate dataplane-token --mesh <mesh> --tag") {
		t.Errorf("warning must name the dataplane-token replacement: %v", warnings)
	}
	if !hasWarning(warnings, "removed fields that 3.0 no longer has in the Mesh schema: constraints") {
		t.Errorf("constraints missing from the dropped-fields summary: %v", warnings)
	}
}

func TestMeshConstraints_V2Advisory(t *testing.T) {
	docs, warnings := transformAll(t, meshWithConstraints, TargetV2)
	spec, _ := docs[0]["spec"].(map[string]interface{})
	if _, ok := spec["constraints"]; !ok {
		t.Errorf("constraints must be kept under v2: %v", spec)
	}
	n := 0
	for _, w := range warnings {
		if strings.Contains(w, "constraints.dataplaneProxy") {
			n++
			if !strings.Contains(w, "Remove it before upgrading.") || strings.Contains(w, "removed from the output") {
				t.Errorf("v2 advisory wording: %s", w)
			}
		}
	}
	if n != 1 {
		t.Errorf("want exactly one constraints advisory under v2, got %d: %v", n, warnings)
	}
}

func TestMeshConstraints_UniversalV3(t *testing.T) {
	docs, warnings := transformAll(t, `type: Mesh
name: prod
constraints:
  dataplaneProxy:
    requirements:
      - tags: {kuma.io/zone: east}
`, TargetV3)
	if _, ok := docs[0]["constraints"]; ok {
		t.Errorf("constraints must be removed from a Universal Mesh under v3: %v", docs[0])
	}
	if !hasWarning(warnings, `Mesh "prod": constraints.dataplaneProxy (1 requirements)`) {
		t.Errorf("missing warning: %v", warnings)
	}
}
