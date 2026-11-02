package migrator

import (
	"strings"
	"testing"

	"github.com/Kong/kuma-migrator/pkg/extractor"
)

func topologyBool(b bool) *bool { return &b }

// renderTopology writes one context directory per meta and renders the report.
func renderTopology(t *testing.T, target TargetVersion, metas map[string]extractor.ContextMeta) string {
	t.Helper()
	in := t.TempDir()
	for dir, meta := range metas {
		if err := extractor.WriteContextMetaFile(in, dir, meta); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	writeMarkdown(&b, &MigrationReport{InputDir: in, OutputDir: t.TempDir(), Target: target})
	return b.String()
}

func TestV3TopologyNote_Standalone(t *testing.T) {
	md := renderTopology(t, TargetV3, map[string]extractor.ContextMeta{
		"k-standalone-ctx": {Tool: "kubectl", Context: "k", CPMode: extractor.CPModeStandalone, Environment: "kubernetes"},
	})
	for _, want := range []string{"## Control Plane Topology", "**Blocking.**", "`k-standalone-ctx` (context `k`): `standalone` mode",
		"KUMA_MODE=zone", "controlPlane.mode: zone", "#standalone-mode-removed", "keeps its `-standalone-ctx` name"} {
		if !strings.Contains(md, want) {
			t.Errorf("report should contain %q:\n%s", want, md)
		}
	}
}

func TestV3TopologyNote_KubernetesGlobal(t *testing.T) {
	md := renderTopology(t, TargetV3, map[string]extractor.ContextMeta{
		"g-global-ctx": {Tool: "kumactl", Context: "g", CPMode: extractor.CPModeGlobal, Environment: "kubernetes", StoreType: "kubernetes"},
	})
	for _, want := range []string{"**Blocking.**", "Global control plane on Kubernetes", "environment=universal",
		"PostgreSQL", "kdsGlobalAddress", "#global-control-plane-on-kubernetes-is-no-longer-supported",
		"https://kuma.io/docs/latest/production/cp-deployment/multi-zone/", "GlobalSecret"} {
		if !strings.Contains(md, want) {
			t.Errorf("report should contain %q:\n%s", want, md)
		}
	}
}

func TestV3TopologyNote_NotFlagged(t *testing.T) {
	cases := map[string]extractor.ContextMeta{
		"konnect":           {Tool: "kumactl", Context: "konnect", CPMode: extractor.CPModeGlobal, Environment: "kubernetes", IsKonnect: true},
		"universal-global":  {Tool: "kumactl", Context: "u", CPMode: extractor.CPModeGlobal, Environment: "universal", StoreType: "postgres"},
		"kubectl-pg-global": {Tool: "kubectl", Context: "p", CPMode: extractor.CPModeGlobal, Environment: "universal", StoreType: "postgres"},
		"federated-zone":    {Tool: "kumactl", Context: "z", CPMode: extractor.CPModeZone, Environment: "kubernetes", Federated: topologyBool(true)},
		"kubectl-zone":      {Tool: "kubectl", Context: "z", CPMode: extractor.CPModeZone, Environment: "kubernetes", Federated: topologyBool(false)},
	}
	for name, meta := range cases {
		md := renderTopology(t, TargetV3, map[string]extractor.ContextMeta{"ctx": meta})
		if strings.Contains(md, "Control Plane Topology") {
			t.Errorf("%s: no topology section expected:\n%s", name, md)
		}
	}
}

// A kumactl-read non-federated zone may still be configured as standalone:
// 2.x reports it as zone. Listed as a check, not as blocking.
func TestV3TopologyNote_NonFederatedKumactlZoneIsACheck(t *testing.T) {
	md := renderTopology(t, TargetV3, map[string]extractor.ContextMeta{
		"z-zone-ctx": {Tool: "kumactl", Context: "z", CPMode: extractor.CPModeZone, Environment: "universal", Federated: topologyBool(false)},
	})
	if !strings.Contains(md, "### Zone control planes to check") || !strings.Contains(md, "`z-zone-ctx` (context `z`)") {
		t.Errorf("expected the zone listed as a check:\n%s", md)
	}
	if strings.Contains(md, "**Blocking.**") {
		t.Errorf("a check is not blocking:\n%s", md)
	}
}

// Older kubectl metadata has no environment or store: a kubectl-extracted
// Global read CRDs, so it is Kubernetes-native.
func TestV3TopologyNote_OldKubectlGlobalMetadata(t *testing.T) {
	md := renderTopology(t, TargetV3, map[string]extractor.ContextMeta{
		"old-global-ctx": {Tool: "kubectl", Context: "old", CPMode: extractor.CPModeGlobal},
	})
	if !strings.Contains(md, "Global control plane on Kubernetes") {
		t.Errorf("expected old kubectl Global flagged:\n%s", md)
	}
}

func TestV3TopologyNote_OnlyUnderV3(t *testing.T) {
	md := renderTopology(t, TargetV2, map[string]extractor.ContextMeta{
		"k-standalone-ctx": {Tool: "kubectl", Context: "k", CPMode: extractor.CPModeStandalone},
	})
	if strings.Contains(md, "Control Plane Topology") {
		t.Errorf("v2 report must not carry the 3.0 topology section:\n%s", md)
	}
}
