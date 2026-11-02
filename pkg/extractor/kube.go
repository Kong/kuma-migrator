package extractor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Kong/kuma-migrator/pkg/ui"
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
//
// ExtractViaKubectl extracts all kuma.io/v1alpha1 resources from the cluster
// reachable via the given Kubernetes context, writing one YAML file per resource
// to outputDir. Resources whose kind contains "Insight" are skipped.
//
// meshFilter, when non-empty, restricts extraction to the named mesh only.
// Global-scoped resources (no kuma.io/mesh label) are always extracted.
func ExtractViaKubectl(kubeContext, outputDir, meshFilter, outputFormat string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	skipSet := loadSkipSet(CPEnvKubernetes) // kubectl path is always Kubernetes

	cpInfo := detectKubeCPInfo(kubeContext)
	cpMode := cpInfo.Mode
	// filterMode drives the per-resource origin filter; "" for a non-federated zone.
	filterMode := originFilterMode(cpInfo)
	dirLabel := cpModeDirectoryLabel(kubeContext, cpMode)
	// Environment: the Helm chart always renders KUMA_ENVIRONMENT=kubernetes; a
	// different value means the CP was overridden to run on Universal (e.g. a
	// Global CP on PostgreSQL deployed in a cluster), which the 3.0 topology
	// advisory needs to tell apart from a Kubernetes-native Global.
	metaEnv := CPEnvKubernetes
	if cpInfo.Environment != "" {
		metaEnv = cpInfo.Environment
	}
	meta := contextMetaFor("kubectl", kubeContext, metaEnv, cpInfo, false)
	if err := WriteContextMetaFile(outputDir, dirLabel, meta); err != nil {
		ui.Warn(fmt.Sprintf("could not write context metadata: %v", err))
	}
	var zones []string
	if cpMode == CPModeGlobal {
		zones = listZoneNamesKubectl(kubeContext)
	}

	ui.Header("extract")
	ui.KV("Context:", kubeContext)
	if meshFilter != "" {
		ui.KV("Mesh filter:", meshFilter)
	}
	PrintCPInfo(cpInfo, zones)
	fmt.Println()

	crds, err := listKumaCRDs(kubeContext, skipSet)
	if err != nil {
		return err
	}
	ui.Found(len(crds), "kuma.io/v1alpha1 CRD(s)")
	fmt.Println()

	total := 0
	notes := &extractNotes{}
	for _, crd := range crds {
		n, err := dumpCRDInstances(kubeContext, crd, outputDir, filterMode, dirLabel, meshFilter, notes)
		if err != nil {
			ui.Warn(fmt.Sprintf("%s: %v", crd.Plural, err))
		}
		total += n
	}
	ui.ExtractDone(total, outputDir)
	printExtractNotes(notes)
	return nil
}

// listZoneNamesKubectl returns the names of all Zone resources from the cluster.
func listZoneNamesKubectl(kubeContext string) []string {
	out, err := runKubectl(kubeContext, "get", "zones.kuma.io", "-o", "json")
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
	info := detectKubeCPInfo(kubeContext)
	return info.Mode, info.ZoneName
}

// detectKubeCPInfo is detectKubeCPMode plus the store type (KUMA_STORE_TYPE)
// and zone federation. A zone is federated when KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS
// and KUMA_MULTIZONE_ZONE_NAME are both non-empty (upstream
// Config.IsFederatedZoneCP). The Helm chart always renders
// KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS in zone mode, with an empty value when
// controlPlane.kdsGlobalAddress is unset (kuma deployments/charts/kuma/templates/_helpers.tpl),
// so the value is what counts, not the presence of the variable.
func detectKubeCPInfo(kubeContext string) CPInfo {
	candidates := []struct{ ns, name string }{
		{"kuma-system", "kuma-control-plane"},
		{"kong-mesh-system", "kong-mesh-control-plane"},
	}
	for _, c := range candidates {
		out, err := runKubectl(kubeContext, "get", "deployment", c.name, "-n", c.ns, "-o", "json")
		if err != nil {
			continue
		}
		if info := kubeCPInfoFromDeploymentJSON(out); info.Mode != "" {
			return info
		}
	}
	return CPInfo{}
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

// kubeCPInfoFromDeploymentJSON reads the CP settings from the env of a
// `kubectl get deployment -o json` document. A later duplicate of a variable
// wins, as it does in the running container.
func kubeCPInfoFromDeploymentJSON(out []byte) CPInfo {
	var dep kubeDeployment
	if err := json.Unmarshal(out, &dep); err != nil {
		return CPInfo{}
	}
	var info CPInfo
	var globalAddress string
	for _, container := range dep.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			switch env.Name {
			case "KUMA_MODE":
				info.Mode = strings.ToLower(env.Value)
			case "KUMA_MULTIZONE_ZONE_NAME":
				info.ZoneName = env.Value
			case "KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS":
				globalAddress = env.Value
			case "KUMA_ENVIRONMENT":
				info.Environment = strings.ToLower(env.Value)
			case "KUMA_STORE_TYPE":
				info.StoreType = strings.ToLower(env.Value)
			}
		}
	}
	info.Federated = isFederatedZone(info.Mode, globalAddress, info.ZoneName)
	return info
}

// ---- CRD discovery ----------------------------------------------------------

type kubeCRDList struct {
	Items []kubeCRDItem `json:"items"`
}

type kubeCRDItem struct {
	Spec struct {
		Group string `json:"group"`
		Names struct {
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
	out, err := runKubectl(kubeContext, "get", "crd", "-o", "json")
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

// dumpCRDInstances lists all instances of a CRD kind from the cluster with one
// `kubectl get <plural> -A -o json` call and writes one YAML file per resource
// through writeResourceObject, which applies the filters (CP-generated, origin,
// mesh), strips server-written fields and picks the directory:
//
//	<outputDir>/<cpModeDir>/mesh-<mesh>/<sub>/              mesh-scoped
//	<outputDir>/<cpModeDir>/global-scoped-resources/<sub>/  no mesh
//
// The mesh is read from the kuma.io/mesh label, or from the top-level mesh
// field the legacy CRDs (TrafficPermission, MeshGatewayRoute, …) store it in.
// A Mesh is written under its own mesh directory.
func dumpCRDInstances(kubeContext string, crd crdEntry, outputDir, cpMode, cpModeDir, meshFilter string, notes *extractNotes) (int, error) {
	out, err := runKubectl(kubeContext, "get", crd.Plural, "-A", "-o", "json")
	if err != nil {
		return 0, err
	}

	var list struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return 0, fmt.Errorf("parse %s list: %w", crd.Plural, err)
	}

	count := 0
	for _, item := range list.Items {
		if k, _ := item["kind"].(string); k == "" {
			item["kind"] = crd.Kind
		}
		if _, ok := item["apiVersion"]; !ok {
			item["apiVersion"] = "kuma.io/v1alpha1"
		}
		n, err := writeResourceObject(item, outputDir, nil, cpMode, cpModeDir, "", meshFilter, "kubernetes", notes)
		if err != nil {
			return count, err
		}
		count += n
	}
	return count, nil
}

// ---- kubectl helper ---------------------------------------------------------

// runKubectl is the kubectl runner used by the kubectl extraction path.
// Tests substitute a fake.
var runKubectl = kubectl

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
