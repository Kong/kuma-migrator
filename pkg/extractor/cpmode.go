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
