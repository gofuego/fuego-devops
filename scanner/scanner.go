package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Run walks repoPath looking for DevOps files (Dockerfiles, Kubernetes manifests)
// and copies them into outDir with YAML frontmatter so fuego can parse them.
func Run(repoPath, outDir string) error {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolving repo path: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	return filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			base := d.Name()
			// Skip hidden dirs and common non-content dirs
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, _ := filepath.Rel(repoPath, path)
		name := d.Name()

		switch {
		case isDockerfile(name):
			return emitFile(path, rel, outDir, "dockerfile", name)
		case isKubernetesManifest(name, path):
			return emitFile(path, rel, outDir, "k8s", name)
		}
		return nil
	})
}

func isDockerfile(name string) bool {
	return name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.")
}

func isKubernetesManifest(name, path string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".yaml" && ext != ".yml" {
		return false
	}
	// Heuristic: check if the file contains apiVersion and kind
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "apiVersion:") && strings.Contains(content, "kind:")
}

// emitFile reads the source file and writes it into outDir with frontmatter.
func emitFile(srcPath, relPath, outDir, fileType, name string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", srcPath, err)
	}

	// Build a clean output filename that preserves the repo path structure.
	// e.g. "infra/k8s/deployment.yaml" → "infra--k8s--deployment.yaml.k8s"
	sanitized := strings.ReplaceAll(relPath, string(filepath.Separator), "--")

	var outName string
	switch fileType {
	case "dockerfile":
		outName = sanitized + ".dockerfile"
	case "k8s":
		outName = sanitized + ".k8s"
	default:
		outName = sanitized + "." + fileType
	}

	title := inferTitle(relPath, name, fileType)
	sourcePath := relPath

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: %q\n", title))
	sb.WriteString(fmt.Sprintf("source_path: %q\n", sourcePath))
	sb.WriteString(fmt.Sprintf("resource_kind: %q\n", fileType))
	sb.WriteString("---\n")
	sb.Write(data)

	outPath := filepath.Join(outDir, outName)
	if err := os.WriteFile(outPath, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}

	fmt.Printf("  scanned %s → %s\n", relPath, outName)
	return nil
}

func inferTitle(relPath, name, fileType string) string {
	dir := filepath.Dir(relPath)
	if dir == "." {
		dir = ""
	}

	switch fileType {
	case "dockerfile":
		if dir != "" {
			return fmt.Sprintf("Dockerfile — %s", dir)
		}
		return "Dockerfile"
	case "k8s":
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if dir != "" {
			return fmt.Sprintf("%s — %s", base, dir)
		}
		return base
	default:
		return name
	}
}
