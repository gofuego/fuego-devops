// Package scanner walks a DevOps repository and emits fuego content files for
// the Dockerfiles and Kubernetes manifests it finds.
//
// Kubernetes manifests are rarely authored as the final applied YAML. Most
// real repositories template them through Kustomize (overlays that inject
// namespaces, patches, and image tags) or Helm (charts with Go-template
// values). Scanning the raw source of those trees is misleading: base manifests
// omit namespaces, and the same resource appears many times across overlays. So
// the scanner renders Kustomize overlays (`kustomize build`) and Helm charts
// (`helm template`) and scans their output — the manifests as they would
// actually be applied — falling back to raw scanning for everything else.
package scanner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Run walks repoPath looking for DevOps files (Dockerfiles, Kubernetes
// manifests) and writes them into outDir with YAML frontmatter so fuego can
// parse them. Kustomize overlays and Helm charts are rendered first; their
// output is scanned instead of the raw source.
func Run(repoPath, outDir string) error {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolving repo path: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	trees, err := discoverTemplateTrees(repoPath)
	if err != nil {
		return err
	}

	// Render Kustomize overlays and Helm charts; their rendered manifests
	// replace the raw source under those trees.
	renderTemplateTrees(repoPath, outDir, trees)

	// Raw-scan everything not owned by a Kustomize/Helm tree. Dockerfiles are
	// always scanned (they are never part of a rendered manifest stream).
	return filepath.WalkDir(repoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			base := d.Name()
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
			if trees.owns(filepath.Dir(path)) {
				return nil // rendered via Kustomize/Helm above
			}
			return emitFile(path, rel, outDir, "k8s", name)
		}
		return nil
	})
}

// templateTrees is the set of Kustomize and Helm directories discovered in a
// repo, plus the leaves chosen for rendering.
type templateTrees struct {
	kustomizeDirs map[string]bool // every dir containing a kustomization file
	chartDirs     map[string]bool // every dir containing Chart.yaml
	kustomizeLeaf []string        // overlays not referenced by another kustomization
}

// owns reports whether dir is at or below any Kustomize/Helm tree, meaning its
// raw YAML is rendered elsewhere and should not be scanned directly.
func (t templateTrees) owns(dir string) bool {
	for d := dir; ; {
		if t.kustomizeDirs[d] || t.chartDirs[d] {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

func discoverTemplateTrees(repoPath string) (templateTrees, error) {
	t := templateTrees{
		kustomizeDirs: map[string]bool{},
		chartDirs:     map[string]bool{},
	}

	err := filepath.WalkDir(repoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "kustomization.yaml", "kustomization.yml", "Kustomization":
			t.kustomizeDirs[filepath.Dir(path)] = true
		case "Chart.yaml":
			t.chartDirs[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return t, fmt.Errorf("discovering template trees: %w", err)
	}

	t.kustomizeLeaf = kustomizeLeaves(t.kustomizeDirs)
	return t, nil
}

// kustomizeLeaves returns the top-level overlays worth rendering: the
// kustomizations you would actually `kustomize build`. A directory is excluded
// when it is "reached from outside" — when some kustomization outside its
// subtree references it or any kustomization within it. That single rule drops
// both directly-referenced overlay fragments (overlays/eng/api, pulled in by
// overlays/eng) and base aggregates (base/, whose children base/system etc. are
// pulled into overlays from outside base/), so each environment renders once
// with its namespace injected and bases are never scanned namespace-less.
func kustomizeLeaves(dirs map[string]bool) []string {
	type edge struct{ from, to string }
	var edges []edge
	for dir := range dirs {
		for _, ref := range kustomizeRefs(dir) {
			target := filepath.Clean(filepath.Join(dir, ref))
			if dirs[target] {
				edges = append(edges, edge{from: dir, to: target})
			}
		}
	}

	reachedFromOutside := func(d string) bool {
		for _, e := range edges {
			if isUnder(e.to, d) && !isUnder(e.from, d) {
				return true
			}
		}
		return false
	}

	var leaves []string
	for dir := range dirs {
		if !reachedFromOutside(dir) {
			leaves = append(leaves, dir)
		}
	}
	sort.Strings(leaves)
	return leaves
}

// isUnder reports whether path is at or below base.
func isUnder(path, base string) bool {
	return path == base || strings.HasPrefix(path, base+string(filepath.Separator))
}

// kustomizeRefs reads a kustomization file and returns its local directory
// references (resources, bases, components). File and remote references are
// returned too but harmlessly fail the directory test in the caller.
func kustomizeRefs(dir string) []string {
	var data []byte
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			data = b
			break
		}
	}
	if data == nil {
		return nil
	}
	var k struct {
		Resources  []string `yaml:"resources"`
		Bases      []string `yaml:"bases"`
		Components []string `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &k); err != nil {
		return nil
	}
	return append(append(k.Resources, k.Bases...), k.Components...)
}

// renderTemplateTrees renders each Kustomize leaf and Helm chart and emits the
// resulting manifests. Failures (missing binary, render error) are reported as
// warnings so one bad tree never aborts the whole scan.
func renderTemplateTrees(repoPath, outDir string, t templateTrees) {
	// seen tracks emitted resources by their cluster identity (namespace/kind/
	// name) so a resource declared by many overlays — especially cluster-scoped
	// ClusterRoles pulled into every environment — is documented once.
	seen := map[string]bool{}

	if len(t.kustomizeLeaf) > 0 {
		if _, err := exec.LookPath("kustomize"); err != nil {
			fmt.Println("  warning: kustomize not found on PATH; falling back to raw scan of overlays")
			for d := range t.kustomizeDirs {
				delete(t.kustomizeDirs, d) // un-own so the raw walk picks them up
			}
		} else {
			for _, leaf := range t.kustomizeLeaf {
				renderKustomize(repoPath, outDir, leaf, seen)
			}
		}
	}

	if len(t.chartDirs) > 0 {
		if _, err := exec.LookPath("helm"); err != nil {
			fmt.Println("  warning: helm not found on PATH; skipping Helm charts (install helm to include them)")
		} else {
			for dir := range t.chartDirs {
				renderHelm(repoPath, outDir, dir, seen)
			}
		}
	}
}

func renderKustomize(repoPath, outDir, dir string, seen map[string]bool) {
	out, err := exec.Command("kustomize", "build", dir).Output()
	relDir, _ := filepath.Rel(repoPath, dir)
	if err != nil {
		fmt.Printf("  warning: kustomize build %s failed: %s\n", relDir, runErr(err))
		return
	}
	emitManifestStream(out, relDir, outDir, seen)
}

func renderHelm(repoPath, outDir, dir string, seen map[string]bool) {
	name := filepath.Base(dir)
	out, err := exec.Command("helm", "template", name, dir).Output()
	relDir, _ := filepath.Rel(repoPath, dir)
	if err != nil {
		fmt.Printf("  warning: helm template %s failed: %s\n", relDir, runErr(err))
		return
	}
	emitManifestStream(out, relDir, outDir, seen)
}

// runErr surfaces the most relevant line of a command's stderr — kustomize and
// helm print deprecation noise before the actual "Error:" line, so prefer that.
func runErr(err error) string {
	ee, ok := err.(*exec.ExitError)
	if !ok || len(ee.Stderr) == 0 {
		return err.Error()
	}
	lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "Error:") {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// emitManifestStream splits a multi-document YAML stream and writes each
// Kubernetes resource as its own content file, keyed by its cluster identity
// (namespace/kind/name). Resources already emitted (same identity from another
// overlay) are skipped via seen, so each real cluster object is documented once.
func emitManifestStream(stream []byte, relDir, outDir string, seen map[string]bool) {
	count := 0
	for _, doc := range bytes.Split(stream, []byte("\n---")) {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		var meta struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal(doc, &meta); err != nil || meta.Kind == "" || meta.Metadata.Name == "" {
			continue
		}

		ns := meta.Metadata.Namespace
		nsPart := ns
		if nsPart == "" {
			nsPart = "cluster"
		}
		identity := fmt.Sprintf("%s/%s/%s", nsPart, meta.Kind, meta.Metadata.Name)
		if seen[identity] {
			continue
		}
		seen[identity] = true

		// relPath drives the (unique) output filename and slug.
		relPath := fmt.Sprintf("%s-%s-%s", nsPart, meta.Kind, meta.Metadata.Name)
		title := fmt.Sprintf("%s %s", meta.Kind, meta.Metadata.Name)
		if ns != "" {
			title += " — " + ns
		}
		source := relDir + " (rendered)"

		if err := writeContent(doc, relPath, outDir, "k8s", meta.Kind, title, source); err != nil {
			fmt.Printf("  warning: %s\n", err)
			continue
		}
		count++
	}
	if count > 0 {
		fmt.Printf("  rendered %s → %d manifests\n", relDir, count)
	}
}

func isDockerfile(name string) bool {
	return name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.")
}

func isKubernetesManifest(name, path string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".yaml" && ext != ".yml" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "apiVersion:") && strings.Contains(content, "kind:")
}

// emitFile reads a source file and writes it into outDir with frontmatter.
func emitFile(srcPath, relPath, outDir, fileType, name string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", srcPath, err)
	}
	title := inferTitle(relPath, name, fileType)
	// Non-k8s files have no Kubernetes kind; group them by a humanized file type
	// (e.g. "dockerfile" → "Dockerfile") so /by-kind stays meaningful.
	kind := fileType
	if fileType == "dockerfile" {
		kind = "Dockerfile"
	}
	if err := writeContent(data, relPath, outDir, fileType, kind, title, relPath); err != nil {
		return err
	}
	fmt.Printf("  scanned %s\n", relPath)
	return nil
}

// writeContent writes a single content file: frontmatter (title, source_path,
// resource_kind) followed by the raw resource body. relPath is sanitized into a
// flat, collision-free filename that preserves the repo path structure.
func writeContent(data []byte, relPath, outDir, fileType, kind, title, sourcePath string) error {
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

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: %q\n", title))
	sb.WriteString(fmt.Sprintf("source_path: %q\n", sourcePath))
	sb.WriteString(fmt.Sprintf("resource_kind: %q\n", kind))
	sb.WriteString("---\n")
	sb.Write(data)

	outPath := filepath.Join(outDir, outName)
	if err := os.WriteFile(outPath, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
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
