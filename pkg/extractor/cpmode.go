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

// cpModeLabel returns the directory label for a given CP mode.
// Unknown mode uses "unknown" so files are still organised under a named folder.
func cpModeLabel(mode string) string {
	switch mode {
	case CPModeGlobal, CPModeZone, CPModeStandalone:
		return mode
	default:
		return "unknown"
	}
}

// cpModeDirectoryLabel returns the output directory name for a CP mode.
// For zone CPs, includes the zone name as a suffix (e.g. "zone-eu-west").
// Falls back to cpModeLabel when zoneName is empty.
func cpModeDirectoryLabel(mode, zoneName string) string {
	if mode == CPModeZone && zoneName != "" {
		return "zone-" + zoneName
	}
	return cpModeLabel(mode)
}
