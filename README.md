# fuego-devops

A documentation site generator for **DevOps infrastructure**, built on the
[Fuego](https://github.com/gofuego/fuego) meta-engine.

Point `fuego-devops` at a repository and it discovers the Dockerfiles and
Kubernetes manifests, resolves how they relate, and produces an interactive
site: a per-namespace **infrastructure overview** (what runs, what exposes it,
what it depends on, what builds it), an **architecture diagram**, and a page per
resource — with no config and nothing written back to your repo.

```bash
fuego-devops ./my-infra-repo
```

It is **Kustomize- and Helm-aware**: it renders overlays and charts and
documents the manifests as they would actually be applied — proper namespaces,
no base-template duplication.

fuego-devops ships as both a **CLI** (zero-config: point it at a repo) and an
importable **format pack** (`devops.Pack()`) you can drop into any Fuego project.

---

## Install

```bash
go install github.com/gofuego/fuego-devops/cmd/fuego-devops@latest
```

Requires Go 1.25+. The binary lands in `$GOPATH/bin` (usually `~/go/bin`); make
sure that's on your `PATH`. Or run without installing:

```bash
go run github.com/gofuego/fuego-devops/cmd/fuego-devops@latest ./my-infra-repo build
```

For Kustomize and Helm support, install those tools too:

```bash
brew install kustomize helm    # or your platform's equivalent
```

Both are optional — without them, fuego-devops raw-scans manifests and skips
Helm charts (with a warning).

## Quick start

```bash
fuego-devops ./my-infra-repo                 # scan + dev server with live reload at :8080
fuego-devops ./my-infra-repo build           # scan + build the static site to build/
fuego-devops -site-name "Acme Infra" ./repo build
```

The first positional argument is the repository to scan; the second is `serve`
(default) or `build`.

## What it finds

| Source | How it's detected | Rendered? |
|---|---|---|
| Dockerfiles | `Dockerfile` or `Dockerfile.*` | scanned as-is |
| Kubernetes manifests | `.yaml`/`.yml` containing `apiVersion:` and `kind:` | scanned as-is |
| Kustomize overlays | a directory with `kustomization.yaml` | `kustomize build`, then scanned |
| Helm charts | a directory with `Chart.yaml` | `helm template`, then scanned |

Hidden directories, `node_modules/`, and `vendor/` are skipped.

### Why render Kustomize/Helm?

Raw manifest source is misleading: base manifests omit namespaces, and the same
resource is declared many times across overlays. fuego-devops renders the
top-level overlays (`kustomize build`) and charts (`helm template`) and scans the
output, so:

- **Namespaces are correct** — overlays inject them, so each environment is
  grouped under its real namespace instead of a giant "(cluster-scoped)" bucket.
- **No duplication** — resources are deduplicated by their real cluster identity
  (`namespace/kind/name`), so a `ClusterRole` shared by every environment is
  documented once.

It renders only the overlays nothing else builds on (the leaves), so each
environment is rendered exactly once. A broken overlay is reported and skipped —
it never fails the whole site.

## The generated site

| Page | URL | Contents |
|---|---|---|
| Overview | `/` | Per-namespace summary + the interactive architecture diagram. |
| Resource | `/kubernetes/{slug}/` | One page per Kubernetes resource. |
| Dockerfile | `/dockerfiles/{slug}/` | One page per Dockerfile. |
| By kind | `/by-kind/`, `/by-kind/{kind}/` | Resources grouped by kind. |

The **overview** leads with a per-namespace summary: each workload shows what
**exposes** it (services and the ingresses fronting them), what it **depends on**
(configmaps, secrets, volumes), and what **builds** it (Dockerfiles). Below it,
an **architecture diagram** lays each namespace out in its own labeled box and
offers three views:

- **Architecture** — every resource, grouped by namespace.
- **Traffic Flow** — ingress → service → workload.
- **Dependencies** — workloads and the config/secrets/images they consume.

The layout is computed (not physics-simulated), so it renders identically every
time, never jitters, and environments never overlap. Click a node to jump to its
detail page; scroll to zoom, drag to pan.

## CLI reference

```
fuego-devops [flags] <repo-path> [build|serve]

Flags:
  -site-name string   Site title (default: "DevOps Docs")
  -base-url string    Base URL for the generated site (deploy subpath)
  -output string      Output directory (default: "build")
```

### Examples

```bash
# Local development with live reload
fuego-devops ./my-infra-repo

# CI build with a custom name and a GitHub Pages subpath
fuego-devops -site-name "Acme Infra" -base-url /infra -output public ./repo build

# In GitHub Actions (no Go code needed)
go install github.com/gofuego/fuego-devops/cmd/fuego-devops@latest
fuego-devops -site-name "My Infra" "$GITHUB_WORKSPACE" build
```

fuego-devops never writes into the scanned repo or the working directory — it
scans into a temporary directory and cleans it up, emitting only the build
output.

## Using the devops pack directly

The CLI is a thin wrapper that scans a repo and then drives `devops.Pack()`. If
you already have content (or want infrastructure docs as one section of a larger
Fuego site), use the pack directly — it brings its parsers (the
[fuego-formats](https://github.com/gofuego/fuego-formats) `docker` and
`kubernetes` modules, also usable standalone), theme, routes, taxonomy, and the
overview hook:

```go
package main

import (
	"context"
	"log"

	"github.com/gofuego/fuego-devops"
	"github.com/gofuego/fuego/engine"
)

func main() {
	eng := engine.New()
	eng.Use(devops.Pack())

	err := eng.Build(context.Background(), engine.BuildOptions{
		ContentDir: "scanned-content", // *.k8s / *.dockerfile files with frontmatter
		OutputDir:  "build",
		SiteName:   "My Infrastructure",
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

To scan a repo *and* build in one call from Go, use the higher-level wrapper:

```go
import "github.com/gofuego/fuego-devops"

func main() {
	err := devops.Run("./my-infra-repo", devops.Options{
		SiteName: "My Infrastructure",
		Command:  "build", // or "serve"
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

A file you place in your own `theme/renderers/*.html` overrides the pack's, so
you can restyle any resource type without forking.

## License

See the repository for license details.
