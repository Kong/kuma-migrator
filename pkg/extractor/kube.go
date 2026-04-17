package extractor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bcollard/kuma-migrator/pkg/resource"
)

// ExtractViaKubectl extracts all kuma.io/v1alpha1 resources from the cluster
// reachable via the given Kubernetes context, writing one YAML file per resource
// to outputDir. Resources whose kind contains "Insight" are skipped.
//
// The extraction mirrors this shell pattern:
//
//	kubectl --context <ctx> get crd -o json \
//	  | <filter kuma.io v1alpha1> \
//	  | while read crd; do
//	      kubectl --context <ctx> get <crd> -A -o json | <per item> | kubectl get -o yaml > file.yaml
//	    done
func ExtractViaKubectl(kubeContext, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	skipSet := loadSkipSet()

	cpMode, zoneName := detectKubeCPMode(kubeContext)
	dirLabel := cpModeDirectoryLabel(cpMode, zoneName)
	switch cpMode {
	case CPModeZone:
		fmt.Printf("CP mode:       zone (%s)\n", zoneName)
		fmt.Printf("[WARN] Extracting from a Zone CP. Only resources with kuma.io/origin: zone will be kept.\n")
		fmt.Printf("       For a complete policy set, also run extract against the Global CP.\n")
		fmt.Printf("[INFO] MeshGatewayInstance and MeshGatewayConfig are zone-local and will be extracted here.\n")
		fmt.Printf("[INFO] MeshGateway and route CRDs (MeshHTTPRoute, MeshTCPRoute, MeshGatewayRoute):\n")
		fmt.Printf("       - If created on the Global CP: synced here with kuma.io/origin: global → skipped (extract from Global CP).\n")
		fmt.Printf("       - If created directly on this Zone CP: no origin label → extracted here.\n")
	case CPModeGlobal:
		fmt.Printf("CP mode:       %s\n", cpMode)
		zones := listZoneNamesKubectl(kubeContext)
		if len(zones) > 0 {
			fmt.Printf("Attached zones: %s\n", strings.Join(zones, ", "))
		}
		fmt.Printf("[INFO] MeshGateway and route CRDs created on the Global CP are extracted here.\n")
		fmt.Printf("[INFO] MeshGatewayInstance and MeshGatewayConfig are zone-local and skipped here.\n")
		fmt.Printf("       Run extract against each Zone CP to capture gateway instances.\n")
	case CPModeStandalone:
		fmt.Printf("CP mode:       %s\n", cpMode)
	default:
		fmt.Printf("CP mode:       unknown (could not detect KUMA_MODE) — extracting all resources\n")
	}

	effectiveOutDir := filepath.Join(outputDir, dirLabel)

	crds, err := listKumaCRDs(kubeContext, skipSet)
	if err != nil {
		return err
	}
	fmt.Printf("Found %d kuma.io/v1alpha1 CRD(s) (Insight kinds and skip-list excluded)\n", len(crds))

	total := 0
	for _, crd := range crds {
		n, err := dumpCRDInstances(kubeContext, crd, effectiveOutDir, cpMode)
		if err != nil {
			fmt.Printf("  [WARN] %s: %v\n", crd.Plural, err)
		}
		total += n
	}
	fmt.Printf("\nExtracted %d resource(s) to %s\n", total, effectiveOutDir)
	return nil
}

// listZoneNamesKubectl returns the names of all Zone resources from the cluster.
func listZoneNamesKubectl(kubeContext string) []string {
	out, err := kubectl(kubeContext, "get", "zones.kuma.io", "-o", "json")
	if err != nil {
		return nil
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil
	}
	var names []string
	for _, item := range list.Items {
		if item.Metadata.Name != "" {
			names = append(names, item.Metadata.Name)
		}
	}
	return names
}

// ---- CP mode detection ------------------------------------------------------

// detectKubeCPMode inspects the control plane Deployment in kuma-system (or
// kong-mesh-system) for the KUMA_MODE and KUMA_MULTIZONE_ZONE_NAME environment
// variables. Returns the lower-cased mode and the zone name (non-empty only for
// zone CPs). Returns ("", "") if the deployment cannot be found — callers treat
// "" as global and extract everything.
func detectKubeCPMode(kubeContext string) (mode, zoneName string) {
	candidates := []struct{ ns, name string }{
		{"kuma-system", "kuma-control-plane"},
		{"kong-mesh-system", "kong-mesh-control-plane"},
	}
	for _, c := range candidates {
		m, z := kubeCPModeFromDeployment(kubeContext, c.ns, c.name)
		if m != "" {
			return m, z
		}
	}
	return "", ""
}

type kubeDeployment struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Env []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

func kubeCPModeFromDeployment(kubeContext, ns, name string) (mode, zoneName string) {
	out, err := kubectl(kubeContext, "get", "deployment", name, "-n", ns, "-o", "json")
	if err != nil {
		return "", ""
	}
	var dep kubeDeployment
	if err := json.Unmarshal(out, &dep); err != nil {
		return "", ""
	}
	for _, container := range dep.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			switch env.Name {
			case "KUMA_MODE":
				mode = strings.ToLower(env.Value)
			case "KUMA_MULTIZONE_ZONE_NAME":
				zoneName = env.Value
			}
		}
	}
	return mode, zoneName
}

// ---- CRD discovery ----------------------------------------------------------

type kubeCRDList struct {
	Items []kubeCRDItem `json:"items"`
}

type kubeCRDItem struct {
	Spec struct {
		Group    string `json:"group"`
		Names    struct {
			Kind   string `json:"kind"`
			Plural string `json:"plural"`
		} `json:"names"`
		Versions []struct {
			Name string `json:"name"`
		} `json:"versions"`
	} `json:"spec"`
}

type crdEntry struct {
	Kind   string
	Plural string
}

func listKumaCRDs(kubeContext string, skipSet map[string]bool) ([]crdEntry, error) {
	out, err := kubectl(kubeContext, "get", "crd", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list CRDs: %w", err)
	}

	var list kubeCRDList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse CRD list: %w", err)
	}

	var entries []crdEntry
	for _, item := range list.Items {
		if item.Spec.Group != "kuma.io" {
			continue
		}
		hasV1Alpha1 := false
		for _, v := range item.Spec.Versions {
			if v.Name == "v1alpha1" {
				hasV1Alpha1 = true
				break
			}
		}
		if !hasV1Alpha1 || isInsightKind(item.Spec.Names.Kind) || skipSet[item.Spec.Names.Kind] {
			continue
		}
		entries = append(entries, crdEntry{
			Kind:   item.Spec.Names.Kind,
			Plural: item.Spec.Names.Plural,
		})
	}
	return entries, nil
}

// ---- Per-CRD instance dump --------------------------------------------------

type kubeResourceList struct {
	Items []kubeResourceItem `json:"items"`
}

type kubeResourceItem struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Namespace string            `json:"namespace"`
		Name      string            `json:"name"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
}

func dumpCRDInstances(kubeContext string, crd crdEntry, outputDir string, cpMode string) (int, error) {
	out, err := kubectl(kubeContext, "get", crd.Plural, "-A", "-o", "json")
	if err != nil {
		return 0, err
	}

	var list kubeResourceList
	if err := json.Unmarshal(out, &list); err != nil {
		return 0, fmt.Errorf("parse %s list: %w", crd.Plural, err)
	}

	count := 0
	for _, item := range list.Items {
		// Skip MeshService CRs auto-generated by Kuma from Kubernetes Services.
		if item.Kind == "MeshService" && item.Metadata.Labels["kuma.io/env"] == "kubernetes" {
			continue
		}

		// On a Global CP, skip zone-only kinds (MeshGateway, MeshGatewayInstance, etc.).
		if cpMode == CPModeGlobal && isZoneOnlyKind(item.Kind) {
			continue
		}

		// On a Zone CP, skip resources not originating from this zone.
		// Gateway-local kinds (MeshGateway, MeshHTTPRoute, etc.) may lack the origin label
		// even when zone-local, so they are kept unless explicitly labelled global.
		if cpMode == CPModeZone {
			origin := item.Metadata.Labels["kuma.io/origin"]
			switch {
			case origin == CPModeZone:
				// explicit zone-origin → keep
			case origin == "" && isGatewayLocalKind(item.Kind):
				// gateway-local kinds may lack the label on zone CPs → keep
			default:
				continue
			}
		}

		kind := item.Kind
		if kind == "" {
			kind = crd.Kind
		}
		ns := item.Metadata.Namespace
		name := item.Metadata.Name

		var yamlBytes []byte
		if ns != "" {
			yamlBytes, err = kubectl(kubeContext, "get", crd.Plural, name, "-n", ns, "-o", "yaml")
		} else {
			yamlBytes, err = kubectl(kubeContext, "get", crd.Plural, name, "-o", "yaml")
		}
		if err != nil {
			fmt.Printf("  [WARN] get %s/%s: %v\n", kind, name, err)
			continue
		}

		var filename string
		if ns != "" {
			filename = sanitize(kind+"-"+ns+"-"+name) + ".yaml"
		} else {
			filename = sanitize(kind+"-"+name) + ".yaml"
		}

		sub := resource.KindSubfolder(kind)
		dir := filepath.Join(outputDir, sub)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Printf("  [WARN] mkdir %s: %v\n", dir, err)
			continue
		}
		outPath := filepath.Join(dir, filename)
		if err := os.WriteFile(outPath, yamlBytes, 0644); err != nil {
			fmt.Printf("  [WARN] write %s: %v\n", outPath, err)
			continue
		}
		fmt.Printf("  → %s/%s\n", sub, filename)
		count++
	}
	return count, nil
}

// ---- kubectl helper ---------------------------------------------------------

func kubectl(kubeContext string, args ...string) ([]byte, error) {
	fullArgs := append([]string{"--context", kubeContext}, args...)
	out, err := exec.Command("kubectl", fullArgs...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s", exitErr.Stderr)
		}
		return nil, err
	}
	return out, nil
}
