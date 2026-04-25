package extractor

import (
	"crypto/tls"
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

// TLSSkipVerify disables TLS certificate verification for all HTTP calls made
// by the extractor. Set via the --tls-skip-verify CLI flag before calling any
// Extract function. Not safe for production use.
var TLSSkipVerify bool

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
//
// meshFilter, when non-empty, restricts extraction to the named mesh only.
// Global-scoped resources are always extracted regardless of meshFilter.
func ExtractViaKumactl(contextName, outputDir, meshFilter, outputFormat string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	cpURL, resolvedCtx, bearerToken, err := resolveKumactlContext(contextName)
	if err != nil {
		return err
	}
	ui.Header("extract")
	ui.KV("Context:", resolvedCtx)
	ui.KV("Control plane:", cpURL)
	if isKonnectURL(cpURL) {
		ui.KV("Platform:", "Kong Konnect (hosted)")
	}

	cpMode, zoneName, cpEnv := detectKumactlCPMode(cpURL, bearerToken)
	if cpEnv != "" {
		ui.KV("Environment:", cpEnv)
	}

	skipSet := loadSkipSet(cpEnv)
	dirLabel := cpModeDirectoryLabel(resolvedCtx, cpMode)
	var zones []string
	if cpMode == CPModeGlobal {
		zones = listZoneNamesKumactl(resolvedCtx, cpURL, bearerToken)
	}
	PrintCPModeInfo(cpMode, zoneName, zones)
	fmt.Println()

	// Discover all writable resource types from the CP API, excluding skip-list kinds.
	types, err := listKumaResourceTypes(cpURL, skipSet, bearerToken)
	if err != nil {
		return err
	}
	ui.Found(len(types), "writable resource type(s)")
	fmt.Println()

	// Collect all Mesh names — needed to iterate Mesh-scoped resources.
	meshNames, err := listMeshNames(resolvedCtx, cpURL, bearerToken)
	if err != nil {
		return fmt.Errorf("list meshes: %w", err)
	}

	// When a mesh filter is set, limit extraction to that single mesh.
	loopMeshes := meshNames
	if meshFilter != "" {
		found := false
		for _, m := range meshNames {
			if m == meshFilter {
				found = true
				break
			}
		}
		if !found {
			ui.Warn(fmt.Sprintf("Mesh %q not found on this CP (available: %s) — no mesh-scoped resources will be extracted.",
				meshFilter, strings.Join(meshNames, ", ")))
			loopMeshes = nil
		} else {
			loopMeshes = []string{meshFilter}
		}
		ui.KV("Mesh filter:", meshFilter)
	}
	ui.KV("Meshes found:", ui.MeshNames(meshNames))

	// Partition resource types into mesh-scoped and global-scoped so we can
	// iterate meshes as the outer loop and print one banner per mesh.
	var meshScopedTypes, globalScopedTypes []resourceTypeEntry
	for _, rt := range types {
		if rt.Scope == "Mesh" {
			meshScopedTypes = append(meshScopedTypes, rt)
		} else {
			globalScopedTypes = append(globalScopedTypes, rt)
		}
	}

	total := 0
	var zoneOriginSkips []ZoneOriginSkip

	// Global-scoped resources first (no mesh association).
	for _, rt := range globalScopedTypes {
		n, err := dumpKumactlResources(resolvedCtx, cpURL, bearerToken, rt, "", outputDir, skipSet, cpMode, dirLabel, meshFilter, outputFormat, &zoneOriginSkips)
		if err != nil {
			ui.Warn(fmt.Sprintf("%s: %v", rt.Path, err))
		}
		total += n
	}

	// Mesh-scoped resources: one banner per mesh, then all types for that mesh.
	for _, mesh := range loopMeshes {
		ui.StartMesh(mesh)
		for _, rt := range meshScopedTypes {
			n, err := dumpKumactlResources(resolvedCtx, cpURL, bearerToken, rt, mesh, outputDir, skipSet, cpMode, dirLabel, meshFilter, outputFormat, &zoneOriginSkips)
			if err != nil {
				if isUnknownMeshFlag(err) {
					// API reported Mesh-scoped but kumactl rejects --mesh:
					// fall back to a single global extraction.
					n2, err2 := dumpKumactlResources(resolvedCtx, cpURL, bearerToken, rt, "", outputDir, skipSet, cpMode, dirLabel, meshFilter, outputFormat, &zoneOriginSkips)
					if err2 != nil {
						ui.Warn(fmt.Sprintf("%s: %v", rt.Path, err2))
					}
					total += n2
					break
				}
				ui.Warn(fmt.Sprintf("%s (mesh %s): %v", rt.Path, mesh, err))
				continue
			}
			total += n
		}
	}
	ui.ExtractDone(total, outputDir)
	printZoneOriginSkips(zoneOriginSkips)
	return nil
}

// listZoneNamesKumactl returns the names of all Zone resources.
// For Konnect-hosted CPs it uses direct HTTP; for self-hosted CPs it uses kumactl.
func listZoneNamesKumactl(kumactlCtx, cpURL, bearerToken string) []string {
	var out []byte
	if isKonnectURL(cpURL) {
		base := strings.TrimRight(cpURL, "/")
		base = strings.TrimSuffix(base, "/api")
		body, status, err := authenticatedGet(base+"/zones", bearerToken, 15*time.Second)
		if err != nil || status != http.StatusOK {
			return nil
		}
		out = body
	} else {
		var err error
		out, err = kumactl(kumactlCtx, "get", "zones", "-o", "yaml")
		if err != nil {
			return nil
		}
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

// isKonnectURL reports whether cpURL points to the Kong Konnect SaaS platform.
// Konnect-hosted mesh CPs always use api.konghq.com as the host. They are always
// global CPs — zone CPs are self-hosted and connect to them.
func isKonnectURL(cpURL string) bool {
	return strings.Contains(cpURL, "api.konghq.com")
}

// konnectURLCheck is the predicate used by dumpKumactlResources to decide
// whether to use the direct-HTTP Konnect path. Overridable in tests.
var konnectURLCheck = isKonnectURL

// detectKumactlCPMode returns the lower-cased CP mode ("global", "zone", "standalone"),
// the zone name (non-empty only for zone CPs), and the deployment environment
// ("kubernetes" or "universal"). All three are empty strings on error so callers
// treat the unknowns as safe defaults (extract everything, kubernetes skip-list).
//
// For Konnect-hosted CPs (api.konghq.com) the mode is always "global" and the
// environment is always "kubernetes" — the /config endpoint does not exist on Konnect
// so we detect it from the URL directly.
//
// For self-hosted CPs, calls GET <cpURL>/config (authenticated when bearerToken is set).
func detectKumactlCPMode(cpURL, bearerToken string) (mode, zoneName, environment string) {
	if isKonnectURL(cpURL) {
		return CPModeGlobal, "", CPEnvKubernetes
	}

	url := strings.TrimRight(cpURL, "/") + "/config"
	body, status, err := authenticatedGet(url, bearerToken, 10*time.Second)
	if err != nil || status != http.StatusOK {
		return "", "", ""
	}
	var cfg struct {
		Mode        string `json:"mode"`
		Environment string `json:"environment"`
		Multizone   struct {
			Zone struct {
				Name string `json:"name"`
			} `json:"zone"`
		} `json:"multizone"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return "", "", ""
	}
	return strings.ToLower(cfg.Mode), cfg.Multizone.Zone.Name, strings.ToLower(cfg.Environment)
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

// listKumaResourceTypes calls GET <cpURL>/_resources and returns all policy
// resource types, excluding Insight kinds and the user skip-list.
//
// The readOnly field from /_resources is intentionally ignored: when the Kuma
// API server is configured with ApiServer.ReadOnly=true (common on Global CPs
// in certain deployments) every resource type is reported as readOnly=true,
// which would produce an empty list. The migrator only reads resources; it
// never writes back through this API, so the flag is irrelevant here.
// Insight resources are excluded by name (contains "Insight") instead.
// bearerToken is added as an Authorization header when non-empty.
func listKumaResourceTypes(cpURL string, skipSet map[string]bool, bearerToken string) ([]resourceTypeEntry, error) {
	url := strings.TrimRight(cpURL, "/") + "/_resources"

	body, status, err := authenticatedGet(url, bearerToken, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	if status != http.StatusOK {
		hint := ""
		if status == http.StatusUnauthorized {
			hint = " (check that a valid token is stored in your kumactl config)"
		}
		return nil, fmt.Errorf("GET %s: unexpected status %d%s", url, status, hint)
	}

	var list resourceTypeList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parse /_resources response: %w", err)
	}

	var result []resourceTypeEntry
	for _, rt := range list.Resources {
		if isInsightKind(rt.Name) || skipSet[rt.Name] {
			continue
		}
		result = append(result, rt)
	}
	return result, nil
}

// ---- Mesh name discovery ----------------------------------------------------

// listMeshNames returns the names of all Mesh resources.
// For Konnect-hosted CPs it uses direct HTTP; for self-hosted CPs it uses kumactl.
func listMeshNames(kumactlCtx, cpURL, bearerToken string) ([]string, error) {
	if isKonnectURL(cpURL) {
		base := strings.TrimRight(cpURL, "/")
		base = strings.TrimSuffix(base, "/api")
		body, status, err := authenticatedGet(base+"/meshes", bearerToken, 15*time.Second)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("GET meshes: unexpected status %d", status)
		}
		return parseMeshNamesFromYAML(body), nil
	}
	out, err := kumactl(kumactlCtx, "get", "meshes", "-o", "yaml")
	if err != nil {
		return nil, err
	}
	return parseMeshNamesFromYAML(out), nil
}

// parseMeshNamesFromYAML extracts mesh names from a kumactl YAML stream.
// Supports three formats:
//   - Stream of individual Mesh documents separated by "---"
//   - Kubernetes-style MeshList (single doc with items[])
//   - Universal-style top-level "name" field
//
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
			Name  string `yaml:"name"`
			Items []struct {
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Name string `yaml:"name"`
			} `yaml:"items"`
		}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			continue
		}
		// Kubernetes-style MeshList: kind: MeshList with items[].
		if len(obj.Items) > 0 {
			for _, item := range obj.Items {
				n := item.Metadata.Name
				if n == "" {
					n = item.Name
				}
				if n != "" {
					names = append(names, n)
				}
			}
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
// mesh is the Kuma mesh name for Mesh-scoped resources (empty for Global-scoped).
// cpModeDir is the CP mode directory label.
// meshFilter restricts extraction to the named mesh when non-empty.
// skips, when non-nil, receives ZoneOriginSkip entries for any resource skipped on a
// Global CP because it carries kuma.io/origin: zone.
//
// For Konnect-hosted CPs (cpURL contains api.konghq.com), resources are fetched
// via a direct authenticated HTTP GET with ?format=kubernetes so that the response
// is in Kubernetes format rather than Universal format. For all other CPs the
// kumactl CLI is used.
func dumpKumactlResources(kumactlCtx, cpURL, bearerToken string, rt resourceTypeEntry, mesh, outputDir string, skipSet map[string]bool, cpMode, cpModeDir, meshFilter, outputFormat string, skips *[]ZoneOriginSkip) (int, error) {
	var (
		out []byte
		err error
	)

	if konnectURLCheck(cpURL) {
		// Konnect: direct HTTP GET with format=kubernetes query parameter.
		// The cpURL stored in kumactl config ends with "/api" (the Kuma CP API root),
		// but the Konnect portal REST API lives at the parent path (without "/api").
		// Strip the trailing "/api" so the resource URLs are correct.
		//
		// Resource URL shape (portal base = cpURL minus trailing "/api"):
		//   global-scoped: <base>/<path>?format=kubernetes
		//   mesh-scoped:   <base>/meshes/<mesh>/<path>?format=kubernetes
		base := strings.TrimRight(cpURL, "/")
		base = strings.TrimSuffix(base, "/api")
		var resourceURL string
		if mesh != "" {
			resourceURL = base + "/meshes/" + mesh + "/" + rt.Path + "?format=kubernetes"
		} else {
			resourceURL = base + "/" + rt.Path + "?format=kubernetes"
		}
		var status int
		out, status, err = authenticatedGet(resourceURL, bearerToken, 30*time.Second)
		if err != nil {
			return 0, err
		}
		if status == http.StatusNotFound {
			return 0, nil // resource type not present — not an error
		}
		if status != http.StatusOK {
			return 0, fmt.Errorf("GET %s: unexpected status %d", resourceURL, status)
		}
	} else {
		// Self-hosted CP: delegate to kumactl CLI.
		args := []string{"get", rt.Path, "-o", "yaml"}
		if mesh != "" {
			args = append(args, "--mesh", mesh)
		}
		out, err = kumactl(kumactlCtx, args...)
		if err != nil {
			if isEmptyResult(err) {
				return 0, nil
			}
			return 0, err
		}
	}

	n, err := writeResourceFiles(out, outputDir, skipSet, cpMode, cpModeDir, mesh, meshFilter, outputFormat, skips)
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

// isUnknownMeshFlag returns true when kumactl rejects --mesh for a resource type
// that the /_resources API incorrectly reported as Mesh-scoped. This happens with
// some Global-scoped resources (e.g. meshglobalratelimits, opa-policies) on
// certain Kuma/Konnect versions.
func isUnknownMeshFlag(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "unknown flag: --mesh")
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

// kumactlAPIServer holds the fields needed to reach and authenticate against a CP.
// Two auth approaches are supported:
//   - authType "tokens": bearerToken = authConf["token"]
//   - headers list: scan for key "Authorization", use its value directly (may already
//     include the "Bearer " prefix, as written by `kumactl config control-planes add`).
type kumactlAPIServer struct {
	URL      string            `yaml:"url"`
	AuthType string            `yaml:"authType"`
	AuthConf map[string]string `yaml:"authConf"`
	Headers  []kumactlHeader   `yaml:"headers"`
}

type kumactlHeader struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
}

type kumactlContext struct {
	Name         string `yaml:"name"`
	ControlPlane string `yaml:"controlPlane"`
}

// resolveKumactlContext parses the kumactl config file and returns the CP URL,
// the resolved context name, and the bearer token (empty when not configured).
func resolveKumactlContext(contextName string) (cpURL, resolvedCtx, bearerToken string, err error) {
	configPath := kumactlConfigPath()
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", "", fmt.Errorf("read kumactl config %q: %w", configPath, err)
	}

	var cfg kumactlConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", "", "", fmt.Errorf("parse kumactl config %q: %w", configPath, err)
	}

	if contextName == "" {
		contextName = cfg.CurrentContext
	}
	if contextName == "" {
		return "", "", "", fmt.Errorf("no --kumactl-context given and no currentContext set in %s", configPath)
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
		return "", "", "", fmt.Errorf("context %q not found in %s", contextName, configPath)
	}

	// Resolve control-plane name → URL and bearer token.
	for _, cp := range cfg.ControlPlanes {
		if cp.Name == cpName {
			token := ""
			switch {
			case cp.Coordinates.APIServer.AuthType == "tokens":
				// Classic token auth: authConf["token"] holds the bare token.
				token = cp.Coordinates.APIServer.AuthConf["token"]
			default:
				// Header-based auth written by `kumactl config control-planes add`:
				// headers: [{key: Authorization, value: "Bearer kpat_..."}]
				// The value already includes the "Bearer " prefix.
				for _, h := range cp.Coordinates.APIServer.Headers {
					if strings.EqualFold(h.Key, "Authorization") {
						// Strip "Bearer " prefix so authenticatedGet can add it back
						// uniformly, or pass the bare token if the header had none.
						val := h.Value
						if after, ok := strings.CutPrefix(val, "Bearer "); ok {
							token = after
						} else {
							token = val
						}
						break
					}
				}
			}
			return cp.Coordinates.APIServer.URL, contextName, token, nil
		}
	}
	return "", "", "", fmt.Errorf("control plane %q (linked from context %q) not found in %s", cpName, contextName, configPath)
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

// ---- HTTP helper ------------------------------------------------------------

// authenticatedGet performs a GET request, adding an Authorization: Bearer header
// when bearerToken is non-empty. Returns the response body, HTTP status code, and
// any transport-level error. When TLSSkipVerify is true, certificate verification
// is disabled (useful for self-signed CP admin server certs).
func authenticatedGet(url, bearerToken string, timeout time.Duration) (body []byte, status int, err error) {
	transport := http.DefaultTransport
	if TLSSkipVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	client := &http.Client{Timeout: timeout, Transport: transport}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
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
