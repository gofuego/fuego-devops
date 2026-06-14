package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_FindsDockerfile(t *testing.T) {
	repo := t.TempDir()
	out := t.TempDir()

	// Create a Dockerfile
	os.WriteFile(filepath.Join(repo, "Dockerfile"), []byte("FROM alpine:3.19\nRUN echo hello\n"), 0o644)

	if err := Run(repo, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Fatalf("expected 1 output file, got %d", len(entries))
	}

	data, _ := os.ReadFile(filepath.Join(out, entries[0].Name()))
	content := string(data)
	if !strings.Contains(content, "---") {
		t.Error("expected frontmatter delimiters")
	}
	if !strings.Contains(content, "FROM alpine:3.19") {
		t.Error("expected Dockerfile content preserved")
	}
	if !strings.HasSuffix(entries[0].Name(), ".dockerfile") {
		t.Errorf("expected .dockerfile extension, got %s", entries[0].Name())
	}
}

func TestRun_FindsKubernetesManifest(t *testing.T) {
	repo := t.TempDir()
	out := t.TempDir()

	k8sDir := filepath.Join(repo, "k8s")
	os.MkdirAll(k8sDir, 0o755)

	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 2
`
	os.WriteFile(filepath.Join(k8sDir, "deployment.yaml"), []byte(manifest), 0o644)

	if err := Run(repo, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Fatalf("expected 1 output file, got %d", len(entries))
	}

	if !strings.HasSuffix(entries[0].Name(), ".k8s") {
		t.Errorf("expected .k8s extension, got %s", entries[0].Name())
	}

	data, _ := os.ReadFile(filepath.Join(out, entries[0].Name()))
	content := string(data)
	if !strings.Contains(content, `title:`) {
		t.Error("expected title in frontmatter")
	}
	if !strings.Contains(content, `source_path:`) {
		t.Error("expected source_path in frontmatter")
	}
}

func TestRun_SkipsNonK8sYAML(t *testing.T) {
	repo := t.TempDir()
	out := t.TempDir()

	// A YAML file that's not a K8s manifest
	os.WriteFile(filepath.Join(repo, "config.yaml"), []byte("name: myapp\nport: 8080\n"), 0o644)

	if err := Run(repo, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := os.ReadDir(out)
	if len(entries) != 0 {
		t.Errorf("expected 0 output files, got %d", len(entries))
	}
}

func TestRun_SkipsHiddenDirs(t *testing.T) {
	repo := t.TempDir()
	out := t.TempDir()

	gitDir := filepath.Join(repo, ".git")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)

	if err := Run(repo, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := os.ReadDir(out)
	if len(entries) != 0 {
		t.Errorf("expected 0 files (hidden dir skipped), got %d", len(entries))
	}
}

func TestKustomizeLeaves(t *testing.T) {
	repo := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(repo, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// base/ is an aggregate whose children are pulled into overlays from
	// outside; overlays/eng pulls in its own api fragment plus shared bases.
	write("base/kustomization.yaml", "resources:\n  - api\n  - system\n")
	write("base/api/kustomization.yaml", "resources:\n  - deployment.yaml\n")
	write("base/system/kustomization.yaml", "resources:\n  - ns.yaml\n")
	write("overlays/eng/kustomization.yaml", "namespace: eng\nresources:\n  - ../../base/system\n  - api\n")
	write("overlays/eng/api/kustomization.yaml", "resources:\n  - ../../../base/api\n")
	// a standalone overlay nobody references
	write("argocd/kustomization.yaml", "resources:\n  - app.yaml\n")

	dirs := map[string]bool{}
	for _, d := range []string{"base", "base/api", "base/system", "overlays/eng", "overlays/eng/api", "argocd"} {
		dirs[filepath.Join(repo, d)] = true
	}

	got := kustomizeLeaves(dirs)
	want := map[string]bool{
		filepath.Join(repo, "overlays/eng"): true,
		filepath.Join(repo, "argocd"):       true,
	}
	if len(got) != len(want) {
		t.Fatalf("leaves = %v, want %v", got, want)
	}
	for _, leaf := range got {
		if !want[leaf] {
			t.Errorf("unexpected leaf %s (base aggregates and referenced fragments should be excluded)", leaf)
		}
	}
}

func TestRun_NestedPaths(t *testing.T) {
	repo := t.TempDir()
	out := t.TempDir()

	nested := filepath.Join(repo, "services", "api")
	os.MkdirAll(nested, 0o755)
	os.WriteFile(filepath.Join(nested, "Dockerfile"), []byte("FROM golang:1.22\n"), 0o644)

	if err := Run(repo, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}

	// Should flatten path with --
	if !strings.Contains(entries[0].Name(), "services--api--Dockerfile") {
		t.Errorf("expected flattened path in filename, got %s", entries[0].Name())
	}
}
