package migrator

import (
	"testing"
)

func TestOutputDocFilename_WithNamespace(t *testing.T) {
	doc := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
  namespace: demo
`)
	got := outputDocFilename(doc)
	if got != "MeshTimeout-demo-my-timeout.yaml" {
		t.Errorf("unexpected filename: %q", got)
	}
}

func TestOutputDocFilename_NoNamespace(t *testing.T) {
	doc := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
`)
	got := outputDocFilename(doc)
	if got != "MeshTimeout-my-timeout.yaml" {
		t.Errorf("unexpected filename: %q", got)
	}
}

func TestOutputDocFilename_EmptyDoc(t *testing.T) {
	got := outputDocFilename([]byte(""))
	if got != "unknown.yaml" {
		t.Errorf("expected unknown.yaml, got %q", got)
	}
}
