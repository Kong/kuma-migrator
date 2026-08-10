package extractor

import (
	"fmt"
	"testing"
)

// TestMeshScopedLoop_UnknownMeshFlagDoesNotAbortRemainingTypes is a regression
// test for a silent data-loss bug found against a live Kong Mesh 2.14.3 CP.
//
// /_resources reports some types as Mesh-scoped that kumactl refuses to scope
// with --mesh ("unknown flag: --mesh"). The recovery path fetched that type
// globally and then broke out of the whole type loop, so every type after it was
// never fetched. On the real CP "health-checks" is 5th of 44 mesh-scoped types,
// so 39 types — MeshTimeout, MeshTrafficPermission, MeshOPA, OPAPolicy and the
// rest — were silently dropped while extract still reported success.
//
// The loop must fall back for that one type and carry on.
func TestMeshScopedLoop_UnknownMeshFlagDoesNotAbortRemainingTypes(t *testing.T) {
	types := []resourceTypeEntry{
		{Name: "MeshCircuitBreaker", Path: "meshcircuitbreakers", Scope: "Mesh"},
		{Name: "MeshRetry", Path: "meshretries", Scope: "Mesh"},
		{Name: "HealthCheck", Path: "health-checks", Scope: "Mesh"},
		{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Mesh"},
		{Name: "OPAPolicy", Path: "opa-policies", Scope: "Mesh"},
	}

	var withMesh, withoutMesh []string
	orig := dumpResources
	defer func() { dumpResources = orig }()
	dumpResources = func(_, _, _ string, rt resourceTypeEntry, mesh, _ string, _ map[string]bool, _, _, _, _ string, _ *[]ZoneOriginSkip) (int, error) {
		if mesh != "" {
			withMesh = append(withMesh, rt.Path)
			if rt.Path == "health-checks" {
				return 0, fmt.Errorf("unknown flag: --mesh")
			}
			return 1, nil
		}
		withoutMesh = append(withoutMesh, rt.Path)
		return 1, nil
	}

	total := runMeshScopedTypes([]string{"default"}, types, "ctx", "https://cp", "tok",
		t.TempDir(), map[string]bool{}, CPModeGlobal, "ctx-global-ctx", "", "universal", nil)

	// Every type must have been attempted with --mesh.
	if len(withMesh) != len(types) {
		t.Errorf("expected all %d types attempted with --mesh, got %d: %v", len(types), len(withMesh), withMesh)
	}
	for _, want := range []string{"meshtimeouts", "opa-policies"} {
		found := false
		for _, got := range withMesh {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("type %q after the failing one was never fetched: %v", want, withMesh)
		}
	}
	// The failing type must have been retried once without --mesh.
	if len(withoutMesh) != 1 || withoutMesh[0] != "health-checks" {
		t.Errorf("expected exactly one global retry for health-checks, got %v", withoutMesh)
	}
	// 4 mesh-scoped successes + 1 global fallback.
	if total != 5 {
		t.Errorf("expected total=5, got %d", total)
	}
}

// TestMeshScopedLoop_GlobalFallbackNotRepeatedPerMesh checks the fallback runs
// once overall, not once per mesh — otherwise every mesh re-extracts the same
// unscoped resources on top of each other.
func TestMeshScopedLoop_GlobalFallbackNotRepeatedPerMesh(t *testing.T) {
	types := []resourceTypeEntry{{Name: "HealthCheck", Path: "health-checks", Scope: "Mesh"}}

	var withoutMesh int
	orig := dumpResources
	defer func() { dumpResources = orig }()
	dumpResources = func(_, _, _ string, rt resourceTypeEntry, mesh, _ string, _ map[string]bool, _, _, _, _ string, _ *[]ZoneOriginSkip) (int, error) {
		if mesh != "" {
			return 0, fmt.Errorf("unknown flag: --mesh")
		}
		withoutMesh++
		return 1, nil
	}

	runMeshScopedTypes([]string{"mesh-a", "mesh-b", "mesh-c"}, types, "ctx", "https://cp", "tok",
		t.TempDir(), map[string]bool{}, CPModeGlobal, "ctx-global-ctx", "", "universal", nil)

	if withoutMesh != 1 {
		t.Errorf("global fallback should run once across all meshes, ran %d times", withoutMesh)
	}
}
