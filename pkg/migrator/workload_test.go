package migrator

import (
	"testing"
)

func TestScanWorkloadEnvVars_Deployment(t *testing.T) {
	input := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: demo
spec:
  template:
    spec:
      containers:
        - name: app
          env:
            - name: BACKEND_URL
              value: "backend_demo_svc_3000:80"
            - name: REDIS_ADDR
              value: "redis_demo_svc_6379"
            - name: UNRELATED
              value: "some-other-value"
        - name: sidecar
          env:
            - name: UPSTREAM
              value: "http://frontend_demo_svc_8080:8080/health"
      initContainers:
        - name: init
          env:
            - name: WAIT_FOR
              value: "postgres_infra_svc_5432"
`
	hits, err := ScanWorkloadEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 4 {
		t.Fatalf("expected 4 hits, got %d: %+v", len(hits), hits)
	}

	// Verify first hit (BACKEND_URL with explicit port).
	h := hits[0]
	if h.WorkloadKind != "Deployment" {
		t.Errorf("WorkloadKind = %q, want Deployment", h.WorkloadKind)
	}
	if h.WorkloadName != "my-app" {
		t.Errorf("WorkloadName = %q, want my-app", h.WorkloadName)
	}
	if h.Namespace != "demo" {
		t.Errorf("Namespace = %q, want demo", h.Namespace)
	}
	if h.ContainerName != "app" {
		t.Errorf("ContainerName = %q, want app", h.ContainerName)
	}
	if h.EnvVarName != "BACKEND_URL" {
		t.Errorf("EnvVarName = %q, want BACKEND_URL", h.EnvVarName)
	}
	if h.OldToken != "backend_demo_svc_3000" {
		t.Errorf("OldToken = %q, want backend_demo_svc_3000", h.OldToken)
	}
	if h.ExplicitPort != ":80" {
		t.Errorf("ExplicitPort = %q, want :80", h.ExplicitPort)
	}
	if h.ServiceName != "backend" {
		t.Errorf("ServiceName = %q, want backend", h.ServiceName)
	}
	if h.ServiceNS != "demo" {
		t.Errorf("ServiceNS = %q, want demo", h.ServiceNS)
	}
	if h.ServicePort != "3000" {
		t.Errorf("ServicePort = %q, want 3000", h.ServicePort)
	}

	// BACKEND_URL with explicit :80 → K8s uses :80, not :3000.
	wantK8s := "backend.demo.svc.cluster.local:80"
	if got := h.NewK8sAddress(); got != wantK8s {
		t.Errorf("NewK8sAddress() = %q, want %q", got, wantK8s)
	}
	wantMesh := "backend.demo.svc.<zone>.mesh.local:80"
	if got := h.NewMeshAddress(); got != wantMesh {
		t.Errorf("NewMeshAddress() = %q, want %q", got, wantMesh)
	}
}

func TestScanWorkloadEnvVars_NoExplicitPort(t *testing.T) {
	// When there is no :port suffix, NewK8sAddress uses the svc-encoded port.
	input := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  namespace: prod
spec:
  template:
    spec:
      containers:
        - name: worker
          env:
            - name: REDIS_ADDR
              value: "redis_prod_svc_6379"
`
	hits, err := ScanWorkloadEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if h.ExplicitPort != "" {
		t.Errorf("ExplicitPort should be empty, got %q", h.ExplicitPort)
	}
	if got := h.NewK8sAddress(); got != "redis.prod.svc.cluster.local:6379" {
		t.Errorf("NewK8sAddress() = %q, want redis.prod.svc.cluster.local:6379", got)
	}
	if got := h.NewMeshAddress(); got != "redis.prod.svc.<zone>.mesh.local:6379" {
		t.Errorf("NewMeshAddress() = %q, want redis.prod.svc.<zone>.mesh.local:6379", got)
	}
}

func TestScanWorkloadEnvVars_URLValue(t *testing.T) {
	// Env var contains a full URL with the svc token embedded.
	input := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gateway
  namespace: apps
spec:
  template:
    spec:
      containers:
        - name: gw
          env:
            - name: AUTH_SERVICE
              value: "http://auth_apps_svc_8080:8080/validate"
`
	hits, err := ScanWorkloadEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if h.OldToken != "auth_apps_svc_8080" {
		t.Errorf("OldToken = %q, want auth_apps_svc_8080", h.OldToken)
	}
	if h.ExplicitPort != ":8080" {
		t.Errorf("ExplicitPort = %q, want :8080", h.ExplicitPort)
	}
	if h.ServiceName != "auth" {
		t.Errorf("ServiceName = %q, want auth", h.ServiceName)
	}
	if h.ServiceNS != "apps" {
		t.Errorf("ServiceNS = %q, want apps", h.ServiceNS)
	}
}

func TestScanWorkloadEnvVars_NonWorkload(t *testing.T) {
	// Kuma policy documents must not produce hits.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: timeout
  namespace: kuma-demo
spec:
  targetRef:
    kind: Mesh
`
	hits, err := ScanWorkloadEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for a policy document, got %d", len(hits))
	}
}

func TestScanWorkloadEnvVars_NoLegacyRefs(t *testing.T) {
	// Deployment with env vars that do not contain _svc_ patterns.
	input := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: clean-app
  namespace: demo
spec:
  template:
    spec:
      containers:
        - name: app
          env:
            - name: BACKEND_URL
              value: "backend.demo.svc.cluster.local:3000"
            - name: LOG_LEVEL
              value: "info"
`
	hits, err := ScanWorkloadEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for already-migrated env vars, got %d", len(hits))
	}
}

func TestScanWorkloadEnvVars_CronJob(t *testing.T) {
	input := `
apiVersion: batch/v1
kind: CronJob
metadata:
  name: cleanup-job
  namespace: ops
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: cleaner
              env:
                - name: DB_ADDR
                  value: "postgres_ops_svc_5432"
`
	hits, err := ScanWorkloadEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for CronJob, got %d", len(hits))
	}
	if hits[0].WorkloadKind != "CronJob" {
		t.Errorf("WorkloadKind = %q, want CronJob", hits[0].WorkloadKind)
	}
	if hits[0].ServiceName != "postgres" {
		t.Errorf("ServiceName = %q, want postgres", hits[0].ServiceName)
	}
}

func TestScanWorkloadEnvVars_StatefulSet(t *testing.T) {
	input := `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: cache
  namespace: data
spec:
  template:
    spec:
      containers:
        - name: cache
          env:
            - name: UPSTREAM
              value: "upstream_data_svc_9000:9000"
`
	hits, err := ScanWorkloadEnvVars([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for StatefulSet, got %d", len(hits))
	}
	if hits[0].WorkloadKind != "StatefulSet" {
		t.Errorf("WorkloadKind = %q, want StatefulSet", hits[0].WorkloadKind)
	}
}

func TestEnvVarHit_MappingKey_Deduplication(t *testing.T) {
	// Two hits for the same token+port should produce the same MappingKey.
	h1 := EnvVarHit{OldToken: "redis_demo_svc_6379", ExplicitPort: ":6379"}
	h2 := EnvVarHit{OldToken: "redis_demo_svc_6379", ExplicitPort: ":6379"}
	if h1.MappingKey() != h2.MappingKey() {
		t.Errorf("same token+port should produce same MappingKey: %q vs %q", h1.MappingKey(), h2.MappingKey())
	}

	// Different explicit ports → different keys.
	h3 := EnvVarHit{OldToken: "redis_demo_svc_6379", ExplicitPort: ":80"}
	if h1.MappingKey() == h3.MappingKey() {
		t.Errorf("different ports should produce different MappingKeys")
	}
}

func TestScanKumaAnnotations_YesNo(t *testing.T) {
	input := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: demo
  annotations:
    kuma.io/sidecar-injection: "yes"
    kuma.io/transparent-proxying: "no"
    app.kubernetes.io/name: my-app
spec:
  template:
    spec:
      containers: []
`
	hits, err := ScanKumaAnnotations([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 annotation hits, got %d", len(hits))
	}

	byKey := map[string]AnnotationHit{}
	for _, h := range hits {
		byKey[h.AnnotationKey] = h
	}

	sidecar := byKey["kuma.io/sidecar-injection"]
	if sidecar.OldValue != "yes" || sidecar.NewValue != "true" {
		t.Errorf("sidecar-injection: expected yes→true, got %q→%q", sidecar.OldValue, sidecar.NewValue)
	}

	proxy := byKey["kuma.io/transparent-proxying"]
	if proxy.OldValue != "no" || proxy.NewValue != "false" {
		t.Errorf("transparent-proxying: expected no→false, got %q→%q", proxy.OldValue, proxy.NewValue)
	}

	// Non-kuma annotation should not appear
	if _, ok := byKey["app.kubernetes.io/name"]; ok {
		t.Error("non-kuma annotation should not be returned")
	}
}

func TestScanKumaAnnotations_TrueFalseIgnored(t *testing.T) {
	input := `
apiVersion: v1
kind: Namespace
metadata:
  name: demo
  annotations:
    kuma.io/sidecar-injection: "true"
    kuma.io/mesh: default
`
	hits, err := ScanKumaAnnotations([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits for already-correct annotations, got %d", len(hits))
	}
}

func TestScanKumaAnnotations_NoAnnotations(t *testing.T) {
	input := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers: []
`
	hits, err := ScanKumaAnnotations([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits, got %d", len(hits))
	}
}
