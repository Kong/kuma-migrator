package migrator

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// Universal has no Gateway API: there is no API server to store a Gateway,
// HTTPRoute or TCPRoute, and kumactl rejects them ("invalid resource type").
// Converting a Universal MeshGateway or MeshGatewayRoute to Gateway API therefore
// produces documents nothing can apply. Instead:
//
//   - v2: the source passes through unchanged. MeshGateway/MeshGatewayRoute are
//     valid on 2.14 and the built-in gateway keeps working, so re-applying them is
//     a no-op; an advisory says 3.0 removes them.
//   - v3: the source is returned as rejected by the target (RejectedByTargetError,
//     routed to the manual tree): 3.0 removes the built-in gateway API in full and
//     there is no Universal successor to generate.

// universalGatewayKind returns the source kind when raw is a Universal
// MeshGateway or MeshGatewayRoute, else "".
func universalGatewayKind(raw []byte, scenario Scenario) string {
	if scenario != ScenarioGateway && scenario != ScenarioGatewayRoute {
		return ""
	}
	if !isUniversalShape(raw) {
		return ""
	}
	if scenario == ScenarioGateway {
		return "MeshGateway"
	}
	return "MeshGatewayRoute"
}

// universalGatewayV2Advisory is the v2 note on a Universal MeshGateway or
// MeshGatewayRoute that passes through unchanged.
func universalGatewayV2Advisory(kind, name string) string {
	return fmt.Sprintf(
		"%s %q: kept unchanged — Universal has no Gateway API, so there is no Gateway/HTTPRoute/TCPRoute "+
			"to convert it to, and the %s is valid on Kuma 2.14. Kuma 3.0 removes the built-in gateway "+
			"(MeshGateway, MeshGatewayRoute and Dataplane networking.gateway): before upgrading, replace it "+
			"with a gateway you run as a delegated gateway, and move its routing to MeshHTTPRoute/MeshTCPRoute. "+
			"Re-run with --to-latest v3 for the 3.0 guidance.",
		kind, name, kind)
}

// universalGatewayV3Reason is the reason a Universal MeshGateway or
// MeshGatewayRoute is rejected by a 3.0 control plane.
func universalGatewayV3Reason(kind string) string {
	return fmt.Sprintf(
		"Kuma 3.0 removes the built-in gateway API (%s is an invalid resource type) and Universal has no "+
			"Gateway API to convert it to. Replace the built-in gateway with a delegated gateway you run "+
			"(for example Kong Gateway) on a Dataplane without kuma.io/gateway — 3.0 removes that marking and "+
			"Dataplane networking.gateway outright — and run kuma-dp with --exclude-inbound-ports (or "+
			"redirect.inbound.excludePorts in the transparent proxy config) covering the gateway's listen "+
			"ports, so the traffic it terminates is not redirected into Envoy. Its listeners and host/path "+
			"routing move into the gateway's own configuration; MeshHTTPRoute/MeshTCPRoute targeting the "+
			"gateway's Dataplane can still split or rewrite its outbound traffic to the backends. "+
			"Delete the MeshGateway/MeshGatewayRoute objects before upgrading. Re-run with --to-latest v2 to "+
			"keep them for a 2.x control plane", kind)
}

// transformUniversalGatewayForV3 returns the source document unchanged with a
// RejectedByTargetError naming it.
func transformUniversalGatewayForV3(raw []byte, kind string, target TargetVersion) ([][]byte, []string, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("unmarshal %s: %w", kind, err)
	}
	name := extractNameFromObj(obj)
	reason := universalGatewayV3Reason(kind)
	return [][]byte{raw}, []string{fmt.Sprintf("%s %q: %s.", kind, name, reason)}, &RejectedByTargetError{
		Target:   target,
		Rejected: []RejectedDoc{{Index: 0, Kind: kind, Name: name, Reasons: []string{reason}}},
	}
}
