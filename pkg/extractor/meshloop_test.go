package extractor

import (
	"fmt"
	"testing"
)

// TestMeshScopedLoop_ErrorDoesNotAbortRemainingTypes is a regression test for a
// silent data-loss bug found against a live Kong Mesh 2.14.3 CP: a type that
// failed stopped the loop, so every type after it was never fetched while
// extract still reported success.
func TestMeshScopedLoop_ErrorDoesNotAbortRemainingTypes(t *testing.T) {
	types := []resourceTypeEntry{
		{Name: "MeshCircuitBreaker", Path: "meshcircuitbreakers", Scope: "Mesh"},
		{Name: "MeshRetry", Path: "meshretries", Scope: "Mesh"},
		{Name: "Broken", Path: "broken", Scope: "Mesh"},
		{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Mesh"},
		{Name: "OPAPolicy", Path: "opa-policies", Scope: "Mesh"},
	}

	var fetched []string
	orig := dumpResources
	defer func() { dumpResources = orig }()
	dumpResources = func(_, _, _ string, rt resourceTypeEntry, mesh, _ string, _ map[string]bool, _, _, _, _ string, _ *extractNotes) (int, error) {
		if mesh == "" {
			t.Errorf("mesh-scoped type %s fetched without a mesh", rt.Path)
		}
		fetched = append(fetched, rt.Path)
		if rt.Path == "broken" {
			return 0, fmt.Errorf("boom")
		}
		return 1, nil
	}

	total := runMeshScopedTypes([]string{"default"}, types, "ctx", "https://cp", "tok",
		t.TempDir(), map[string]bool{}, CPModeGlobal, "ctx-global-ctx", "", "universal", nil)

	if len(fetched) != len(types) {
		t.Errorf("expected all %d types attempted, got %v", len(types), fetched)
	}
	if total != 4 {
		t.Errorf("expected total=4, got %d", total)
	}
}
