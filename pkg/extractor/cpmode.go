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
	// Environment is the CP deployment environment: CPEnvKubernetes or
	// CPEnvUniversal. Empty when it could not be detected (GET /config failed)
	// and in metadata written before this field existed. migrate uses it to pick
	// the default skip list for the context.
	// The kubectl path always records CPEnvKubernetes: its output is CRDs.
	Environment string `json:"environment,omitempty"`
	// StoreType is the CP resource store ("kubernetes", "postgres", "memory").
	// kumactl path: GET /config store.type. kubectl path: KUMA_STORE_TYPE on the
	// CP Deployment. Empty when unknown. Read by the 3.0 topology advisory: 3.0
	// rejects a Global CP on a Kubernetes store.
	StoreType string `json:"storeType,omitempty"`
	// Federated is set for zone-mode CPs only: true when the zone is connected
	// to a Global CP (Global address and zone name both set, the same test as
	// upstream Config.IsFederatedZoneCP), false for a zone CP that runs on its
	// own. nil for other modes and in metadata written before this field existed.
	Federated *bool `json:"federated,omitempty"`
}

// ContextMetaFile is the name of the metadata file in each context directory.
const ContextMetaFile = ".kuma-migrator.json"

// WriteContextMeta creates the context directory and writes a .kuma-migrator.json
// metadata file recording the extraction parameters. environment is
// CPEnvKubernetes, CPEnvUniversal or "" when unknown.
func WriteContextMeta(outputDir, dirLabel, tool, contextName, cpMode, environment string, isKonnect bool) error {
	return WriteContextMetaFile(outputDir, dirLabel, ContextMeta{
		Tool:        tool,
		Context:     contextName,
		CPMode:      cpMode,
		IsKonnect:   isKonnect,
		Environment: environment,
	})
}

// WriteContextMetaFile creates the context directory and writes meta as its
// .kuma-migrator.json file.
func WriteContextMetaFile(outputDir, dirLabel string, meta ContextMeta) error {
	dir := filepath.Join(outputDir, dirLabel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal context meta: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, ContextMetaFile), data, 0644)
}

// ReadContextMeta reads the .kuma-migrator.json metadata file from a context
// directory. Returns nil if the file is absent (older extract output).
func ReadContextMeta(inputDir, cpModeDir string) *ContextMeta {
	if cpModeDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(inputDir, cpModeDir, ContextMetaFile))
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

// StoreTypeKubernetes is the store.type (KUMA_STORE_TYPE) of a CP that keeps
// its resources as CRDs.
const StoreTypeKubernetes = "kubernetes"

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

// CPInfo is what extract learns about a control plane before fetching
// resources.
type CPInfo struct {
	Mode        string // CPModeGlobal / CPModeZone / CPModeStandalone / "" (unknown)
	ZoneName    string // zone CPs only
	Environment string // CPEnvKubernetes / CPEnvUniversal / "" (unknown)
	StoreType   string // "kubernetes", "postgres", "memory", "" (unknown)
	// Federated mirrors upstream Config.IsFederatedZoneCP. Only meaningful when
	// Mode is zone.
	Federated bool
}

// IsNonFederatedZone reports whether the CP is a zone CP that is not connected
// to a Global CP. Such a zone owns every resource in its store, so extract
// applies no origin filter to it.
func (c CPInfo) IsNonFederatedZone() bool {
	return c.Mode == CPModeZone && !c.Federated
}

// isFederatedZone mirrors upstream Config.IsFederatedZoneCP
// (kuma pkg/config/app/kuma-cp/config.go): zone mode with a Global KDS address
// and a zone name.
func isFederatedZone(mode, globalAddress, zoneName string) bool {
	return mode == CPModeZone && globalAddress != "" && zoneName != ""
}

// originFilterMode returns the mode passed to the per-resource origin filter
// (writeResourceFiles / dumpCRDInstances). A non-federated zone gets "", which
// applies no origin filter: no Global CP feeds it, so every resource in its
// store is its own, labelled or not.
func originFilterMode(info CPInfo) string {
	if info.IsNonFederatedZone() {
		return ""
	}
	return info.Mode
}

// contextMetaFor builds the metadata record written for a context.
func contextMetaFor(tool, contextName, environment string, info CPInfo, isKonnect bool) ContextMeta {
	meta := ContextMeta{
		Tool:        tool,
		Context:     contextName,
		CPMode:      info.Mode,
		IsKonnect:   isKonnect,
		Environment: environment,
		StoreType:   info.StoreType,
	}
	if info.Mode == CPModeZone {
		f := info.Federated
		meta.Federated = &f
	}
	return meta
}

// ContextDir is one extracted context: its directory name relative to the
// migrate input directory ("" when the input directory is itself a context
// directory) and its metadata.
type ContextDir struct {
	Dir  string
	Meta ContextMeta
}

// ListContextMetas returns the metadata of every context directory directly
// under inputDir, plus inputDir itself when it carries a metadata file, sorted
// by directory name. Directories without metadata (hand-made or older input)
// are left out.
func ListContextMetas(inputDir string) []ContextDir {
	var out []ContextDir
	if meta := readContextMetaFile(filepath.Join(inputDir, ContextMetaFile)); meta != nil {
		out = append(out, ContextDir{Dir: "", Meta: *meta})
	}
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return out
	}
	for _, e := range entries { // os.ReadDir sorts by name
		if !e.IsDir() {
			continue
		}
		if meta := ReadContextMeta(inputDir, e.Name()); meta != nil {
			out = append(out, ContextDir{Dir: e.Name(), Meta: *meta})
		}
	}
	return out
}

func readContextMetaFile(path string) *ContextMeta {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var meta ContextMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

// V3TopologyIssue classifies an extracted control plane against the 3.0
// topology requirements (kuma UPGRADE.md, "Upgrade to 3.0.0").
type V3TopologyIssue string

const (
	// V3TopologyOK: nothing to change.
	V3TopologyOK V3TopologyIssue = ""
	// V3TopologyStandalone: KUMA_MODE / controlPlane.mode is standalone, which
	// 3.0 rejects at startup ("`standalone` mode removed"). Blocking.
	V3TopologyStandalone V3TopologyIssue = "standalone"
	// V3TopologyKubernetesGlobal: a Global CP with environment=kubernetes or
	// store.type=kubernetes, which 3.0 kuma-cp rejects ("Global control plane on
	// Kubernetes is no longer supported"). Blocking.
	V3TopologyKubernetesGlobal V3TopologyIssue = "kubernetes-global"
	// V3TopologyCheckMode: a non-federated zone read through GET /config. A 2.x
	// kuma-cp started with KUMA_MODE=standalone rewrites it to zone at startup
	// (kuma app/kuma-cp/cmd/run.go) and reports zone, so the configured value
	// cannot be seen from the API. Not blocking: the operator has to check.
	V3TopologyCheckMode V3TopologyIssue = "check-mode"
)

// V3Topology reports whether this control plane can be upgraded to 3.0 as it
// is deployed. Konnect's hosted Global CP is not operator-deployed and is
// never flagged.
//
// The kubectl path reads CRDs, so a kubectl-extracted Global with no recorded
// environment or store (older metadata) is treated as Kubernetes-native.
func (m ContextMeta) V3Topology() V3TopologyIssue {
	switch m.CPMode {
	case CPModeStandalone:
		return V3TopologyStandalone
	case CPModeGlobal:
		if m.IsKonnect {
			return V3TopologyOK
		}
		if m.Environment == CPEnvKubernetes || m.StoreType == StoreTypeKubernetes {
			return V3TopologyKubernetesGlobal
		}
		if m.Tool == "kubectl" && m.Environment == "" && m.StoreType == "" {
			return V3TopologyKubernetesGlobal
		}
	case CPModeZone:
		if m.Tool == "kumactl" && m.Federated != nil && !*m.Federated {
			return V3TopologyCheckMode
		}
	}
	return V3TopologyOK
}
