package extractor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kong/kuma-migrator/pkg/ui"
	"sigs.k8s.io/yaml"
)

// ExtractViaKumactl extracts all writable Kuma resources from the CP reachable
// via the given kumactl context, writing one YAML file per resource to outputDir.
//
// Extraction flow:
//  1. Resolve context → CP URL from ~/.kumactl/config (or $KUMACTL_CONFIG)
//  2. GET <cpURL>/_resources → discover all writable resource types
//  3. List all Mesh names via kumactl
//  4. For Mesh-scoped types: kumactl get <path> --mesh <mesh> -o yaml  (per mesh)
//  5. For Global-scoped types: kumactl get <path> -o yaml
//  6. Split YAML stream → one file per resource (Insight kinds skipped)
func ExtractViaKumactl(contextName, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	skipSet := loadSkipSet()

	cpURL, resolvedCtx, err := resolveKumactlContext(contextName)
	if err != nil {
		return err
	}
	ui.Header("extract")
	ui.KV("Context", resolvedCtx)
	ui.KV("Control plane", cpURL)

	cpMode, zoneName := detectKumactlCPMode(cpURL)
	dirLabel := cpModeDirectoryLabel(cpMode, zoneName)
	var zones []string
	if cpMode == CPModeGlobal {
		zones = listZoneNamesKumactl(resolvedCtx)
	}
	PrintCPModeInfo(cpMode, zoneName, zones)
	fmt.Println()

	effectiveOutDir := filepath.Join(outputDir, dirLabel)

	// Discover all writable resource types from the CP API, excluding skip-list kinds.
	types, err := listKumaResourceTypes(cpURL, skipSet)
	if err != nil {
		return err
	}
	ui.Found(len(types), "writable resource type(s)")
	fmt.Println()

	// Collect all Mesh names — needed to iterate Mesh-scoped resources.
	meshNames, err := listMeshNames(resolvedCtx)
	if err != nil {
		return fmt.Errorf("list meshes: %w", err)
	}

	total := 0
	for _, rt := range types {
		if rt.Scope == "Mesh" {
			for _, mesh := range meshNames {
				n, err := dumpKumactlResources(resolvedCtx, rt, mesh, effectiveOutDir, skipSet, cpMode)
				if err != nil {
					ui.Warn(fmt.Sprintf("%s (mesh %s): %v", rt.Path, mesh, err))
				}
				total += n
			}
		} else {
			n, err := dumpKumactlResources(resolvedCtx, rt, "", effectiveOutDir, skipSet, cpMode)
			if err != nil {
				ui.Warn(fmt.Sprintf("%s: %v", rt.Path, err))
			}
			total += n
		}
	}
	ui.ExtractDone(total, effectiveOutDir)
	return nil
}

// listZoneNamesKumactl returns the names of all Zone resources via kumactl.
func listZoneNamesKumactl(kumactlCtx string) []string {
	out, err := kumactl(kumactlCtx, "get", "zones", "-o", "yaml")
	if err != nil {
		return nil
	}
	// Response is a Kuma list: top-level "items" array, each item has a "name" field.
	var list struct {
		Items []struct {
			Name string `yaml:"name"`
		} `yaml:"items"`
	}
	if err := yaml.Unmarshal(out, &list); err != nil {
		return nil
	}
	var names []string
	for _, item := range list.Items {
		if item.Name != "" {
			names = append(names, item.Name)
		}
	}
	return names
}

// ---- CP mode detection ------------------------------------------------------

// detectKumactlCPMode calls GET <cpURL>/config and returns the lower-cased CP mode
// ("global", "zone", "standalone") and, for zone CPs, the zone name from
// multizone.zone.name. Returns ("", "") on any error so callers treat it as
// unknown and fall back to extracting everything.
func detectKumactlCPMode(cpURL string) (mode, zoneName string) {
	url := strings.TrimRight(cpURL, "/") + "/config"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", ""
	}
	var cfg struct {
		Mode       string `json:"mode"`
		Multizone  struct {
			Zone struct {
				Name string `json:"name"`
			} `json:"zone"`
		} `json:"multizone"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return "", ""
	}
	return strings.ToLower(cfg.Mode), cfg.Multizone.Zone.Name
}

// ---- CP resource-type discovery ---------------------------------------------

type resourceTypeEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Scope    string `json:"scope"`    // "Global" | "Mesh"
	ReadOnly bool   `json:"readOnly"` // true for Insights and computed resources
}

type resourceTypeList struct {
	Resources []resourceTypeEntry `json:"resources"`
}

// listKumaResourceTypes calls GET <cpURL>/_resources and returns all types
// where readOnly is false. ReadOnly resources (Insights, etc.) are skipped.
func listKumaResourceTypes(cpURL string, skipSet map[string]bool) ([]resourceTypeEntry, error) {
	url := strings.TrimRight(cpURL, "/") + "/_resources"

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read /_resources response: %w", err)
	}

	var list resourceTypeList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parse /_resources response: %w", err)
	}

	var result []resourceTypeEntry
	for _, rt := range list.Resources {
		if !rt.ReadOnly && !skipSet[rt.Name] {
			result = append(result, rt)
		}
	}
	return result, nil
}

// ---- Mesh name discovery ----------------------------------------------------

// listMeshNames returns the names of all Mesh resources via kumactl.
func listMeshNames(kumactlCtx string) ([]string, error) {
	out, err := kumactl(kumactlCtx, "get", "meshes", "-o", "yaml")
	if err != nil {
		return nil, err
	}
	return parseMeshNamesFromYAML(out), nil
}

// parseMeshNamesFromYAML extracts mesh names from a kumactl YAML stream.
// Supports both Kubernetes-style (metadata.name) and Universal-style (top-level name).
// Falls back to ["default"] when no names can be parsed.
func parseMeshNamesFromYAML(data []byte) []string {
	docs := splitYAMLDocs(data)
	var names []string
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var obj struct {
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			continue
		}
		name := obj.Metadata.Name
		if name == "" {
			name = obj.Name
		}
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		// Default mesh always exists; fall back if kumactl returns nothing parseable.
		return []string{"default"}
	}
	return names
}

// ---- Per-type resource dump -------------------------------------------------

// dumpKumactlResources fetches all instances of a resource type (optionally scoped
// to a mesh) and writes one YAML file per resource to outputDir.
func dumpKumactlResources(kumactlCtx string, rt resourceTypeEntry, mesh, outputDir string, skipSet map[string]bool, cpMode string) (int, error) {
	args := []string{"get", rt.Path, "-o", "yaml"}
	if mesh != "" {
		args = append(args, "--mesh", mesh)
	}

	out, err := kumactl(kumactlCtx, args...)
	if err != nil {
		// A "not found" / empty result is not an error worth surfacing.
		if isEmptyResult(err) {
			return 0, nil
		}
		return 0, err
	}

	n, err := writeResourceFiles(out, outputDir, skipSet, cpMode)
	return n, err
}

// isEmptyResult returns true when the kumactl error message indicates there are
// simply no resources of that type (as opposed to a real API or auth error).
func isEmptyResult(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no resources found") ||
		strings.Contains(msg, "not found")
}

// ---- kumactl config parsing -------------------------------------------------

// kumactlConfig mirrors the ~/.kumactl/config YAML structure.
type kumactlConfig struct {
	ControlPlanes  []kumactlControlPlane `yaml:"controlPlanes"`
	Contexts       []kumactlContext      `yaml:"contexts"`
	CurrentContext string                `yaml:"currentContext"`
}

type kumactlControlPlane struct {
	Name        string             `yaml:"name"`
	Coordinates kumactlCoordinates `yaml:"coordinates"`
}

type kumactlCoordinates struct {
	APIServer kumactlAPIServer `yaml:"apiServer"`
}

// kumactlAPIServer holds only the URL field; other fields (caCertFile, authType,
// authConf) are present in real configs but not needed for the CLI-based extraction.
type kumactlAPIServer struct {
	URL string `yaml:"url"`
}

type kumactlContext struct {
	Name         string `yaml:"name"`
	ControlPlane string `yaml:"controlPlane"`
}

// resolveKumactlContext parses the kumactl config file and returns the CP URL and
// the resolved context name (which may differ from the input when contextName is "").
func resolveKumactlContext(contextName string) (cpURL, resolvedCtx string, err error) {
	configPath := kumactlConfigPath()
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", fmt.Errorf("read kumactl config %q: %w", configPath, err)
	}

	var cfg kumactlConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", "", fmt.Errorf("parse kumactl config %q: %w", configPath, err)
	}

	if contextName == "" {
		contextName = cfg.CurrentContext
	}
	if contextName == "" {
		return "", "", fmt.Errorf("no --kumactl-context given and no currentContext set in %s", configPath)
	}

	// Resolve context → control-plane name.
	cpName := ""
	for _, ctx := range cfg.Contexts {
		if ctx.Name == contextName {
			cpName = ctx.ControlPlane
			break
		}
	}
	if cpName == "" {
		return "", "", fmt.Errorf("context %q not found in %s", contextName, configPath)
	}

	// Resolve control-plane name → URL.
	for _, cp := range cfg.ControlPlanes {
		if cp.Name == cpName {
			return cp.Coordinates.APIServer.URL, contextName, nil
		}
	}
	return "", "", fmt.Errorf("control plane %q (linked from context %q) not found in %s", cpName, contextName, configPath)
}

// kumactlConfigPath returns the path to the kumactl config file, honouring the
// KUMACTL_CONFIG environment variable if set.
func kumactlConfigPath() string {
	if p := os.Getenv("KUMACTL_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kumactl", "config")
}

// ---- kumactl helper ---------------------------------------------------------

func kumactl(kumactlCtx string, args ...string) ([]byte, error) {
	fullArgs := append([]string{"--context", kumactlCtx}, args...)
	out, err := exec.Command("kumactl", fullArgs...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s", exitErr.Stderr)
		}
		return nil, err
	}
	return out, nil
}
