package extractor

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"
)

// KumactlTarget is a control plane resolved from a kumactl context: what another
// REST client (such as the 3.0 readiness audit) needs to reach it.
type KumactlTarget struct {
	// Context is the resolved kumactl context name.
	Context string
	// URL is the control plane URL as written in the kumactl config.
	URL string
	// Address is the REST API base: URL, minus the trailing "/api" on Konnect
	// (kumactl talks to .../control-planes/<id>/api, the resource endpoints are
	// served one level up).
	Address string
	// Token is the bearer token (no "Bearer " prefix), empty when none is set.
	Token string
	// Konnect is true for a Konnect-hosted control plane.
	Konnect bool
}

// ResolveKumactlTarget resolves a kumactl context (empty = the current context)
// from ~/.kumactl/config or $KUMACTL_CONFIG.
func ResolveKumactlTarget(contextName string) (KumactlTarget, error) {
	cpURL, resolvedCtx, token, err := resolveKumactlContext(contextName)
	if err != nil {
		return KumactlTarget{}, err
	}
	return kumactlTargetFor(resolvedCtx, cpURL, token), nil
}

func kumactlTargetFor(resolvedCtx, cpURL, token string) KumactlTarget {
	t := KumactlTarget{Context: resolvedCtx, URL: cpURL, Token: token, Konnect: isKonnectURL(cpURL)}
	t.Address = strings.TrimRight(cpURL, "/")
	if t.Konnect {
		t.Address = strings.TrimSuffix(t.Address, "/api")
	}
	return t
}

// NewHTTPClient returns the client the extractor's own requests use: TLSSkipVerify
// honoured, redirects never followed (a control plane cannot bounce the bearer
// token to another host). See authenticatedGet for the threat model.
func NewHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport
	if TLSSkipVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// TargetFromURL builds a target from a URL given on the command line (Konnect
// detected from the host, "/api" stripped as for a kumactl context).
func TargetFromURL(cpURL, token string) KumactlTarget {
	return kumactlTargetFor("", cpURL, token)
}
