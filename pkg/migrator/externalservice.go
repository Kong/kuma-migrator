package migrator

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// ---- Old ExternalService structs (input) -------------------------------------

type oldExternalService struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	// Universal style: flat top-level fields.
	Type string `json:"type"`
	Name string `json:"name"`
	Mesh string `json:"mesh"`
	Tags map[string]string `json:"tags"`
	Networking *oldESNetworking `json:"networking"`
	// Kubernetes style: under metadata/spec.
	Metadata *struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec *struct {
		Tags       map[string]string `json:"tags"`
		Networking *oldESNetworking  `json:"networking"`
	} `json:"spec"`
}

type oldESNetworking struct {
	Address string    `json:"address"`
	TLS     *oldESTLS `json:"tls"`
}

type oldESTLS struct {
	Enabled            bool            `json:"enabled"`
	AllowRenegotiation bool            `json:"allowRenegotiation"`
	ServerName         string          `json:"serverName"`
	CACert             json.RawMessage `json:"caCert,omitempty"`
	ClientCert         json.RawMessage `json:"clientCert,omitempty"`
	ClientKey          json.RawMessage `json:"clientKey,omitempty"`
}

// ---- Main transformer --------------------------------------------------------

// TransformExternalService converts an old ExternalService CRD (both Kubernetes-style
// and Universal-style) into a new MeshExternalService CRD.
func TransformExternalService(raw []byte) ([][]byte, []string, error) {
	var es oldExternalService
	if err := yaml.Unmarshal(raw, &es); err != nil {
		return nil, nil, fmt.Errorf("unmarshal ExternalService: %w", err)
	}

	name, namespace, mesh := extractESMeta(&es)
	tags := getESTags(&es)
	networking := getESNetworking(&es)

	if name == "" {
		return nil, nil, fmt.Errorf("ExternalService has no name")
	}
	if networking == nil || networking.Address == "" {
		return nil, nil, fmt.Errorf("ExternalService %q has no networking.address", name)
	}

	// Unix socket support was removed from MeshExternalService in Kuma 2.9.
	if strings.HasPrefix(networking.Address, "unix://") || strings.HasPrefix(networking.Address, "/") {
		return nil, nil, fmt.Errorf("ExternalService %q: unix socket address %q is not supported in MeshExternalService (removed in 2.9) — migrate this ExternalService manually", name, networking.Address)
	}

	host, portStr, err := splitHostPort(networking.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("ExternalService %q: invalid address %q: %w", name, networking.Address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, nil, fmt.Errorf("ExternalService %q: port %q is not a number: %w", name, portStr, err)
	}

	protocol := "tcp"
	if p, ok := tags["kuma.io/protocol"]; ok && p != "" {
		protocol = strings.ToLower(p)
	}

	// Build spec.
	spec := map[string]interface{}{
		"match": map[string]interface{}{
			"type":     "HostnameGenerator",
			"port":     port,
			"protocol": protocol,
		},
		"endpoints": []interface{}{
			map[string]interface{}{
				"address": host,
				"port":    port,
			},
		},
	}

	var warnings []string

	if w := ValidateResourceName(name, "MeshExternalService"); w != "" {
		warnings = append(warnings, w)
	}

	// TLS migration.
	if networking.TLS != nil {
		spec["tls"] = buildMESTLSSpec(networking.TLS)
		warnings = append(warnings, fmt.Sprintf(
			"MeshExternalService %q: TLS config migrated — verify spec.tls.verification settings "+
				"(verification.mode defaults to Secured; adjust if needed)", name))
	}

	// Metadata.
	labels := map[string]interface{}{}
	if mesh != "" {
		labels["kuma.io/mesh"] = mesh
	}
	meta := map[string]interface{}{
		"name":   name,
		"labels": labels,
	}
	if namespace != "" {
		meta["namespace"] = namespace
	}

	output := map[string]interface{}{
		"apiVersion": kumaAPIVersion,
		"kind":       "MeshExternalService",
		"metadata":   meta,
		"spec":       spec,
	}

	warnings = append(warnings, fmt.Sprintf(
		"MeshExternalService %q: spec.match.port is set to %d (same as the endpoint port). "+
			"Update spec.match.port if clients should connect on a different virtual port.", name, port))
	warnings = append(warnings, fmt.Sprintf(
		"MeshExternalService %q: a HostnameGenerator will create a DNS name from the resource name — "+
			"verify the generated hostname matches what consumers expect.", name))

	b, err := yaml.Marshal(output)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal MeshExternalService: %w", err)
	}
	return [][]byte{b}, warnings, nil
}

// ---- TLS builder -------------------------------------------------------------

func buildMESTLSSpec(tls *oldESTLS) map[string]interface{} {
	result := map[string]interface{}{
		"enabled": tls.Enabled,
	}
	if tls.AllowRenegotiation {
		result["allowRenegotiation"] = tls.AllowRenegotiation
	}

	verification := map[string]interface{}{
		"mode": "Secured",
	}
	if tls.ServerName != "" {
		verification["serverName"] = tls.ServerName
	}
	if len(tls.CACert) > 0 && string(tls.CACert) != "null" {
		var v interface{}
		if json.Unmarshal(tls.CACert, &v) == nil {
			verification["caCert"] = v
		}
	}
	if len(tls.ClientCert) > 0 && string(tls.ClientCert) != "null" {
		var v interface{}
		if json.Unmarshal(tls.ClientCert, &v) == nil {
			verification["clientCert"] = v
		}
	}
	if len(tls.ClientKey) > 0 && string(tls.ClientKey) != "null" {
		var v interface{}
		if json.Unmarshal(tls.ClientKey, &v) == nil {
			verification["clientKey"] = v
		}
	}
	result["verification"] = verification
	return result
}

// ---- Helpers -----------------------------------------------------------------

func extractESMeta(es *oldExternalService) (name, namespace, mesh string) {
	mesh = es.Mesh
	if es.Metadata != nil {
		name = es.Metadata.Name
		namespace = es.Metadata.Namespace
		if mesh == "" {
			if m, ok := es.Metadata.Labels["kuma.io/mesh"]; ok {
				mesh = m
			}
		}
	}
	if name == "" {
		name = es.Name
	}
	if namespace == "" {
		namespace = "kong-mesh-system"
	}
	// Universal style has no namespace concept — clear it.
	if es.Kind == "" && es.Type != "" {
		namespace = ""
	}
	return name, namespace, mesh
}

func getESTags(es *oldExternalService) map[string]string {
	if es.Spec != nil && len(es.Spec.Tags) > 0 {
		return es.Spec.Tags
	}
	return es.Tags
}

func getESNetworking(es *oldExternalService) *oldESNetworking {
	if es.Spec != nil && es.Spec.Networking != nil {
		return es.Spec.Networking
	}
	return es.Networking
}

// splitHostPort wraps net.SplitHostPort but also handles bare "host:port" without brackets.
func splitHostPort(address string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(address)
	if err != nil {
		// Last resort: try splitting on the last colon.
		if idx := strings.LastIndex(address, ":"); idx != -1 {
			host = address[:idx]
			port = address[idx+1:]
			err = nil
		}
	}
	return host, port, err
}
