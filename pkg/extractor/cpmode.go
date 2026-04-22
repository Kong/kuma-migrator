package extractor

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
