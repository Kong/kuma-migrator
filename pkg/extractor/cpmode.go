package extractor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ContextMeta is written by kuma-migrator extract into each context directory
// as <outputDir>/<cpModeDir>/.kuma-migrator.json. It records how the extraction
// was performed so that migrate/plan can generate correct apply instructions.
type ContextMeta struct {
	Tool      string `json:"tool"`                // "kubectl" or "kumactl"
	Context   string `json:"context"`             // original context name
	CPMode    string `json:"cpMode"`              // "global", "zone", "standalone", ""
	IsKonnect bool   `json:"isKonnect,omitempty"` // true when the CP is Kong Konnect hosted
}

// WriteContextMeta creates the context directory and writes a .kuma-migrator.json
// metadata file recording the extraction parameters.
func WriteContextMeta(outputDir, dirLabel, tool, contextName, cpMode string, isKonnect bool) error {
	dir := filepath.Join(outputDir, dirLabel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}
	meta := ContextMeta{
		Tool:      tool,
		Context:   contextName,
		CPMode:    cpMode,
		IsKonnect: isKonnect,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal context meta: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, ".kuma-migrator.json"), data, 0644)
}

// ReadContextMeta reads the .kuma-migrator.json metadata file from a context
// directory. Returns nil if the file is absent (older extract output).
func ReadContextMeta(inputDir, cpModeDir string) *ContextMeta {
	if cpModeDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(inputDir, cpModeDir, ".kuma-migrator.json"))
	if err != nil {
		return nil
	}
	var meta ContextMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

const (
	CPModeGlobal     = "global"
	CPModeZone       = "zone"
	CPModeStandalone = "standalone"
)

// CPEnvKubernetes and CPEnvUniversal are the two deployment environments
// reported by GET /config under the "environment" field.
const (
	CPEnvKubernetes = "kubernetes"
	CPEnvUniversal  = "universal"
)

// Output directory naming conventions shared between the extractor and migrator.
const (
	// GlobalScopedDir is the level-2 subdirectory that holds global-scoped resources
	// (those with no mesh association: Zone, HostnameGenerator, Gateway API CRDs, …).
	// It sits at <outputDir>/<cpModeDir>/global-scoped-resources/<sub>/.
	GlobalScopedDir = "global-scoped-resources"

	// MeshDirPrefix is prepended to the Kuma mesh name when forming the level-2
	// subdirectory for mesh-scoped resources, e.g. "mesh-default".
	MeshDirPrefix = "mesh-"
)

// cpModeDirectoryLabel returns the top-level output directory label for a CP
// extraction. The name combines the kumactl/kubectl context name with a suffix
// that encodes the CP mode, making it easy to identify the origin of each file
// set when multiple CPs are extracted into the same parent directory.
//
//	contextName + "-global-ctx"     — Global CP
//	contextName + "-zone-ctx"       — Zone CP
//	contextName + "-standalone-ctx" — Standalone CP
//	contextName + "-unknown-ctx"    — mode could not be detected
func cpModeDirectoryLabel(contextName, mode string) string {
	switch mode {
	case CPModeGlobal:
		return contextName + "-global-ctx"
	case CPModeZone:
		return contextName + "-zone-ctx"
	case CPModeStandalone:
		return contextName + "-standalone-ctx"
	default:
		return contextName + "-unknown-ctx"
	}
}
