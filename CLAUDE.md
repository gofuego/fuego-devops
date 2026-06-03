# CLAUDE.md — fuego-devops Contributor Guide

## What is fuego-devops?

fuego-devops is a **domain-specific static site generator for DevOps infrastructure**, built on top of the [Fuego](https://github.com/FabioSol/fuego) meta-engine. It scans a repository for Dockerfiles and Kubernetes manifests, extracts their structure and relationships, and produces an interactive documentation site with canvas-based architecture diagrams.

The core value proposition: **point it at an infrastructure repo, get an interactive architecture diagram and per-resource documentation with zero configuration**.

## How it uses Fuego

fuego-devops is a concrete SSG built using Fuego's extension points. It does **not** modify fuego core. Everything works through the public API:

| Fuego extension point | fuego-devops usage |
|---|---|
| `core.Parser` | Kubernetes manifest parser (`parser.Kubernetes()`) |
| `core.FilenameParser` | Dockerfile parser (`parser.Dockerfile()`) |
| `core.BeforeRenderHook` | Graph builder (`graph.BuildGraphHook`) |
| `theme/` templates | Embedded dark-theme with vis.js diagram layout |
| `core.WithYAMLFrontmatter` | Wraps both parsers for scanner-generated frontmatter |
| `core.WithNoEnvelope` | Not used — both content types carry frontmatter from the scanner |

## Architecture Decisions

### AD-1: Scanner produces fuego-compatible content files

**Decision:** The scanner (`scanner.Run`) walks a repository and emits files into a `content/` directory with YAML frontmatter and the original file contents as the body. Dockerfiles get a `.dockerfile` extension, K8s manifests get `.k8s`.

**Why:** Fuego discovers content by extension and dispatches to parsers. The scanner bridges the gap between arbitrary repo layouts and fuego's content model. By writing frontmatter-wrapped files, the scanner provides metadata (`title`, `source_path`, `resource_kind`) without requiring the parsers to understand repo structure.

### AD-2: Relationship data extracted during parsing, not in the graph hook

**Decision:** Parsers emit specialized node types that carry relationship data — `pod-template-labels`, `env-ref`, `service-spec.selectorMap`, `volume.refName`, `ingress-rule.serviceName`. The graph hook reads these nodes to build edges.

**Why:** Parsers have access to the raw manifest structure and can extract relationship fields precisely. The graph hook operates on the already-parsed `[]core.Node` slice across all pages — it doesn't re-parse YAML. This keeps the graph hook simple: it pattern-matches on node types and attributes rather than understanding YAML semantics.

### AD-3: Virtual overview page via BeforeRender hook

**Decision:** `BuildGraphHook` creates a virtual `*core.Page` with `Type: "diagram"` and a single `graph-data` node whose Attributes carry the full `{nodes, edges}` structure. This page is appended to the page list.

**Why:** This follows the same pattern as fuego's taxonomy builder — virtual pages go through the standard RENDER phase. The diagram layout template reads the graph data from `{{.JSON}}` and initializes vis.js. No changes to fuego core are needed.

### AD-4: Embedded theme with materialization

**Decision:** The theme directory is embedded in the Go binary via `//go:embed all:theme` and materialized to disk at runtime. The `Run()` function writes theme files before invoking the fuego engine.

**Why:** This eliminates the need for consumers to symlink or copy theme files. The binary is self-contained — `go install` produces a working CLI. Theme files are overwritten on each run to stay in sync with the library version.

### AD-5: Generated config, not user-authored

**Decision:** `Run()` generates `config.yaml` with routes, taxonomies, and directory mappings. Users never write this file — they pass `Options` (site name, base URL, output dir) and the rest is handled.

**Why:** Routes (`/dockerfiles/{slug}`, `/kubernetes/{slug}`, `/`) and taxonomies (`resource_kind`) are fuego-devops conventions, not user choices. Generating the config removes a class of copy-paste errors and keeps the consumer's project minimal.

### AD-6: Two usage modes — Go API and CLI binary

**Decision:** fuego-devops exposes both a Go API (`devops.Run(repoPath, opts)`) and a CLI binary (`fuego-devops [flags] <repo-path> [build|serve]`). The CLI delegates to the Go API.

**Why:** The Go API serves library consumers (like `acme-docs`) who want programmatic control. The CLI serves CI pipelines that need to generate docs without writing Go code. Both share the same `Run()` function.

## Project Structure

```
fuego-devops/
  devops.go                Top-level API: Run(), Options, theme embedding, config generation
  cmd/fuego-devops/        CLI binary entry point
  scanner/                 Repository scanning — walks repos, emits content files
  parser/
    kubernetes.go          K8s manifest parser — emits structured nodes for all resource kinds
    dockerfile.go          Dockerfile parser — emits stage/instruction nodes
  graph/
    graph.go               BeforeRender hook — builds infrastructure graph from parsed pages
  theme/
    base.html              HTML shell (dark theme, vis.js CDN)
    layouts/
      default.html         Per-resource detail page layout
      diagram.html         Full-viewport interactive graph layout (vis.js)
    renderers/             Per-node-type HTML templates (17 renderers)
```

## Package Responsibilities

### `devops` (root package)

The public API. `Run(repoPath string, opts Options) error` is the single entry point that orchestrates:
1. `scanner.Run()` — scan repo into `content/`
2. `materializeTheme()` — extract embedded theme to `theme/`
3. `writeConfig()` — generate `config.yaml` with defaults + user overrides
4. Engine setup — register parsers, hook
5. `eng.Run()` — delegate to fuego

### `scanner`

Walks a repository, identifies DevOps files, and emits fuego-compatible content files with YAML frontmatter.

- **Dockerfile detection:** filename is `Dockerfile` or `Dockerfile.*`
- **K8s detection:** `.yaml`/`.yml` files containing both `apiVersion:` and `kind:`
- **Skips:** hidden directories, `node_modules/`, `vendor/`
- **Output naming:** path separators become `--` (e.g., `infra/prod/deploy.yaml` becomes `infra--prod--deploy.yaml.k8s`)

### `parser`

Two parsers that produce structured `[]core.Node` for rendering and graph building.

**`Kubernetes()`** — Parses YAML manifests. Dispatches by `kind`:
- Workloads (Deployment, StatefulSet, DaemonSet, Job, CronJob) — emits `resource-header`, `metadata`, `replicas`, `pod-template-labels`, `container-spec`, `env-ref`, `volume`
- Service — emits `service-spec`, `port-mapping`
- ConfigMap — emits `config-data`
- Secret — emits `secret-data` (values redacted)
- Ingress — emits `ingress-rule`
- Unknown kinds — emits `spec` (raw YAML)

**`Dockerfile()`** — Parses Dockerfiles line-by-line. Implements `FilenameParser` for extensionless `Dockerfile` files. Emits `stage`, `instruction`, `comment` nodes. Tracks multi-stage builds (`COPY --from=`) and collects base images into the envelope.

### `graph`

`BuildGraphHook` is a `core.BeforeRenderHook` that:
1. Indexes all K8s and Dockerfile pages
2. Builds lookup maps for fast resource resolution
3. Constructs edges by analyzing node attributes:
   - **Ingress to Service** (`routes-to`): `ingress-rule.serviceName`
   - **Service to Workload** (`selects`): `service-spec.selectorMap` matched against `pod-template-labels`
   - **Workload to ConfigMap/Secret** (`env-from`): `env-ref.refKind` + `env-ref.refName`
   - **Workload to ConfigMap/Secret/PVC** (`mounts`): `volume.refName`
   - **Dockerfile to Workload** (`builds`): base image name matches container image name (tags stripped)
4. Creates a virtual overview page at `/` with the full graph as a `graph-data` node

## Node Type Reference

### Kubernetes nodes

| Node type | Emitted by | Key attributes | Used by graph |
|-----------|-----------|----------------|---------------|
| `resource-header` | All kinds | `kind`, `apiVersion`, `name`, `namespace` | Yes — builds graph node ID |
| `metadata` | All kinds | label/annotation key-value pairs | No |
| `replicas` | Workloads | `count` | No |
| `pod-template-labels` | Workloads | label key-value pairs | Yes — Service selector matching |
| `container-spec` | Workloads | `name`, `image`, `ports`, `limits`, `requests`, `envCount`, `volumeMounts` | Yes — Dockerfile image matching |
| `env-ref` | Workloads | `refKind`, `refName`, `container` | Yes — ConfigMap/Secret edges |
| `volume` | Workloads | `name`, `volumeType`, `refName` | Yes — volume mount edges |
| `service-spec` | Service | `serviceType`, `selector`, `selectorMap` | Yes — workload selection |
| `port-mapping` | Service | `port`, `targetPort`, `protocol` | No |
| `config-data` | ConfigMap | `key` (content is value) | No |
| `secret-data` | Secret | `keys` array (values redacted) | No |
| `ingress-rule` | Ingress | `host`, `path`, `pathType`, `serviceName`, `servicePort` | Yes — Service edges |
| `spec` | Unknown kinds | content is YAML | No |

### Dockerfile nodes

| Node type | Key attributes | Used by graph |
|-----------|----------------|---------------|
| `stage` | `image`, `alias` | No (images go in envelope) |
| `instruction` | `instruction`, `stage`, `copyFrom` | No |
| `comment` | (content is text) | No |

## Theme

The theme is embedded in the binary and materialized at runtime.

### Layouts

- **`default.html`** — Per-resource detail page. Shows title, source path link, and rendered node content.
- **`diagram.html`** — Full-viewport interactive graph. Three view modes switched client-side:
  - **Architecture** — force-directed layout (`forceAtlas2Based`), all resources
  - **Traffic Flow** — hierarchical left-to-right, only Ingress/Service/Workload
  - **Dependencies** — hierarchical top-down, Workload/ConfigMap/Secret/PVC
  - Click a node to navigate to its detail page. Hover for kind/name/namespace tooltip.

### Renderers (17)

One HTML template per node type. Located in `theme/renderers/{type}.html`. Each receives a `core.Node` as template data. The `graph-data` renderer is intentionally empty (its data is consumed by the diagram layout's JavaScript, not rendered as HTML).

### vis.js

Loaded via CDN (`unpkg.com/vis-network/standalone/umd/vis-network.min.js`) in `base.html`. The diagram layout reads graph data from `{{.JSON}}`, builds vis.js DataSets, and maps resource kinds to shapes/colors:

| Kind | Shape | Color |
|------|-------|-------|
| Deployment, StatefulSet, DaemonSet | box | blue |
| Service | ellipse | green |
| Ingress | diamond | orange |
| ConfigMap | box | purple |
| Secret | box | red |
| Dockerfile | hexagon | gray |
| Other | box | teal |

## CLI Usage

```
fuego-devops [flags] <repo-path> [build|serve]

Flags:
  -site-name string    Site title (default: "DevOps Docs")
  -base-url string     Base URL for the generated site
  -output string       Output directory (default: "build")
```

### Examples

```bash
# Local development with live reload
fuego-devops ./my-infra-repo

# CI build with custom name
fuego-devops -site-name="Acme Infra" -base-url="/docs" ./repo build

# GitHub Actions (no Go code needed)
go install github.com/FabioSol/fuego-devops/cmd/fuego-devops@latest
mkdir site && cd site
fuego-devops -site-name="My Infra" $GITHUB_WORKSPACE build
```

### Go API

```go
import devops "github.com/FabioSol/fuego-devops"

func main() {
    err := devops.Run("../my-infra-repo", devops.Options{
        SiteName: "My Infrastructure",
        Command:  "build",
    })
}
```

## Generated Artifacts

`Run()` creates these in the working directory (not in the scanned repo):

| Path | Generated by | Committed? |
|------|-------------|------------|
| `content/` | scanner | No — regenerated each run |
| `theme/` | materializeTheme | No — overwritten from embedded FS |
| `config.yaml` | writeConfig | No — regenerated each run |
| `public/` | ensured by Run | No — empty, required by fuego |
| `build/` | fuego engine | No — output artifact |

For consumer projects, `.gitignore` should include: `content/`, `theme/`, `config.yaml`, `build/`, `public/`.

## Common Tasks

### Adding support for a new K8s kind

1. Add a case in `parseKubernetes()` in `parser/kubernetes.go`
2. Emit nodes with types matching existing renderers (or create new ones)
3. If the kind has relationship data (selectors, refs), emit nodes that `graph/graph.go` can consume
4. Add edge-building logic in `buildGraph()` if needed
5. Add a renderer in `theme/renderers/{type}.html` for any new node types
6. Update tests in `parser/kubernetes_test.go`

### Adding a new edge type to the graph

1. Ensure the parser emits a node with the relationship data in its attributes
2. Add edge-building logic in `buildGraph()` in `graph/graph.go`
3. Use `addEdge(from, to, edgeType, label)` which handles deduplication
4. Add tests in `graph/graph_test.go`
5. Optionally update `diagram.html` if the new edge type needs filtering in specific views

### Adding a new file type (e.g., Helm charts, Terraform)

1. Create a new parser in `parser/{name}.go` implementing `core.Parser`
2. Update `scanner/scanner.go` to detect the new file type and emit with the correct extension
3. Register the parser in `devops.go` (`eng.Register(parser.NewParser())`)
4. Add a route in `writeConfig()` for the new type
5. Create renderers in `theme/renderers/` for any new node types
6. Update `graph/graph.go` if the new type participates in relationship edges

### Customizing the theme

The embedded theme is overwritten on each `Run()`. To customize:
- Fork fuego-devops and modify files in `theme/` directly, or
- Use the Go API and call the fuego engine directly (bypassing `Run()`) with your own theme directory

## External Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/FabioSol/fuego` | Meta-engine SSG — pipeline, routing, rendering, serving |
| `gopkg.in/yaml.v3` | YAML parsing for Kubernetes manifests |

All other dependencies (cobra, doublestar, fsnotify, etc.) are transitive through fuego.

## What NOT to Do

- **Don't modify fuego core for fuego-devops features.** Everything works through parsers, hooks, and templates. If you need a new fuego capability, add it to fuego's public API first.
- **Don't hardcode resource relationships in the theme.** The graph hook builds all edges programmatically. The theme only visualizes what the hook provides.
- **Don't parse YAML in the graph hook.** Relationship data should be extracted by parsers and stored in node attributes. The graph hook reads attributes, not raw content.
- **Don't hand-edit generated files** (`config.yaml`, `theme/`, `content/`). They are overwritten on each run. Change the source: `devops.go` for config/theme, `scanner/` for content.
