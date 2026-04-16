package extractor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	crds, err := listKumaCRDs(kubeContext)
	if err != nil {
		return err
	}
	fmt.Printf("Found %d kuma.io/v1alpha1 CRD(s) (Insight kinds excluded)\n", len(crds))

	total := 0
	for _, crd := range crds {
		n, err := dumpCRDInstances(kubeContext, crd, outputDir)
		if err != nil {
			fmt.Printf("  [WARN] %s: %v\n", crd.Plural, err)
		}
		total += n
	}
	fmt.Printf("\nExtracted %d resource(s) to %s\n", total, outputDir)
	return nil
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

func listKumaCRDs(kubeContext string) ([]crdEntry, error) {
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
		if !hasV1Alpha1 || isInsightKind(item.Spec.Names.Kind) {
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
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"metadata"`
}

func dumpCRDInstances(kubeContext string, crd crdEntry, outputDir string) (int, error) {
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

		outPath := filepath.Join(outputDir, filename)
		if err := os.WriteFile(outPath, yamlBytes, 0644); err != nil {
			fmt.Printf("  [WARN] write %s: %v\n", outPath, err)
			continue
		}
		fmt.Printf("  → %s\n", filename)
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
