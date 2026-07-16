# CLAUDE.md — fuego-devops Contributor Guide

## What is fuego-devops?

fuego-devops is a **domain-specific static site generator for DevOps
infrastructure**, built on the [Fuego](https://github.com/gofuego/fuego)
meta-engine. Point it at a repository; it discovers the Dockerfiles and
Kubernetes manifests, resolves their relationships, and produces an interactive
documentation site: a per-namespace infrastructure overview, an architecture
diagram, and a page per resource.

It is structured as a **Fuego format pack** (`devops.Pack()`) plus a **scanner**
front-end and a thin CLI. The pack renders already-discovered content; the
scanner turns an arbitrary repo into content the pack can consume. `devops.Run`
and the CLI wire the two together.

## How it uses Fuego

fuego-devops does **not** fork or modify Fuego. Everything works through Fuego
v0.3's public extension points:

| Fuego extension point | fuego-devops usage |
|---|---|
| `core.Parser` | `kubernetes.Parser()` (from fuego-formats) parses `.k8s` manifests |
| `core.FilenameParser` | `docker.Parser()` (from fuego-formats) parses `Dockerfile` / `.dockerfile` |
| `core.Pack` (`eng.Use`) | `devops.Pack()` bundles parsers + theme + config + hook |
| `Pack.Theme fs.FS` | embedded `theme/` (templates + `static/`, vis-network) |
| `Pack.ConfigDefaults` | `config-defaults.yaml` (routes + `resource_kind` taxonomy) |
| `core.IndexHook` | `graph.BuildOverviewHook` builds the overview virtual page |
| `engine.Build/Serve` | the programmatic build API that `devops.Run` drives |

If something here feels limiting, the fix usually belongs in Fuego's pack API,
not in a workaround here (see "What NOT to Do").

## Authored vs. extracted content

This is the defining difference from a pack like fuego-adr. ADRs are **authored**
— a human writes `*.adr.md` files and the pack reads them directly. Infrastructure
manifests are **extracted** — nobody writes "the site"; the truth lives in a repo
of Dockerfiles, raw manifests, Kustomize overlays, and Helm charts. So
fuego-devops adds a **scanner** stage in front of the pack that turns that repo
into pack-consumable content files. The pack itself never sees the repo.

## Architecture Decisions

### AD-1: A format pack behind a scanner front-end

**Decision:** All rendering logic (parsers, theme, config defaults, the graph
hook) lives in `devops.Pack()`. A separate `scanner` package turns a repo into
content files, and `devops.Run` composes them: scan to a temp dir →
`engine.New()` → `eng.Use(devops.Pack())` → `eng.Build`/`eng.Serve`.

**Why:** The pack is the v0.3 unit of reuse and stays repo-agnostic — anyone can
`eng.Use(devops.Pack())` over a directory of already-emitted content, or compose
it into a larger site. The scanner is the only part that knows about repo layout,
Kustomize, and Helm. Keeping them separate means the rendering contract is
testable without a scanner and the scanner is testable without rendering.

### AD-2: The scanner renders Kustomize/Helm before scanning

**Decision:** The scanner does not raw-read every YAML. It discovers
`kustomization.yaml` directories and `Chart.yaml` directories, runs
`kustomize build` / `helm template`, and scans the **rendered** manifest stream.
Files outside any templating tree are raw-scanned; Dockerfiles are always
scanned.

**Why:** Raw source is misleading. Base manifests omit namespaces, and the same
resource is declared many times across overlays — scanning source produces a
giant "(cluster-scoped)" bucket and massive duplication. Rendering resolves both:
overlays inject namespaces, so each environment renders once, correctly grouped.
See AD-2a/2b for the two rules that make this reliable.

### AD-2a: Render only "leaf" overlays, found by reach-from-outside

**Decision:** `kustomizeLeaves` builds the kustomization reference graph and
renders only directories **not reached from outside their own subtree** — i.e.
nothing outside them references them or their children.

**Why:** This single rule excludes both referenced fragments (`overlays/eng/api`,
pulled into `overlays/eng`) and base aggregates (`base/`, whose children are
pulled into overlays from outside `base/`). The result: each environment overlay
renders exactly once, with its namespace injected, and bases are never scanned
namespace-less.

### AD-2b: Deduplicate by cluster identity

**Decision:** `emitManifestStream` keys every emitted resource by
`namespace/kind/name` in a `seen` set shared across all renders, skipping repeats.

**Why:** A `ClusterRole` named `prometheus` is one object in the cluster no matter
how many overlays declare it. Namespaced resources are naturally unique per
namespace; cluster-scoped ones would otherwise repeat once per overlay. Dedup by
real cluster identity documents each object once.

### AD-2c: Graceful degradation when binaries are absent

**Decision:** If `kustomize` is not on `PATH`, overlays fall back to raw scanning
(with a warning). If `helm` is absent, charts are skipped with a warning. A
single overlay that fails to build warns (showing the `Error:` line from stderr)
and is skipped — it never aborts the scan.

**Why:** The tool must be useful on a machine without the full toolchain, and one
broken overlay in a large repo shouldn't take down the whole site.

### AD-3: Parsers extract relationships; the graph hook reads attributes

**Decision:** Parsers emit specialized node types carrying relationship data —
`k8s-pod-template-labels`, `k8s-env-ref`, `k8s-service-spec.selectorMap`,
`k8s-volume.refName`, `k8s-ingress-rule.serviceName`,
`k8s-container-spec.image`. The graph hook reads these node attributes; it
never re-parses YAML.

**Why:** Parsers have the raw structure and extract relationship fields precisely.
The hook then pattern-matches on node types/attributes across all pages, keeping
it free of YAML semantics.

### AD-8: The parsers live in fuego-formats; this repo contains no parser code

**Decision:** The Dockerfile and Kubernetes parsers are the
`github.com/gofuego/fuego-formats/docker` and `.../kubernetes` modules;
`devops.Pack()` registers `docker.Parser()` and `kubernetes.Parser()`, and the
graph hook reads their exported node-type constants. The attribute contract
the hook depends on is documented in each module's `schema.md` as public API.

**Why:** The parsers are reusable beyond this pack (any Fuego site can
register them standalone), and fuego-formats is the ecosystem's home for
independently-versioned format parsers. Node types gained the `docker-`/`k8s-`
prefixes in the move (renderer templates renamed to match); page types are
`docker` and `k8s`. The docker module's `*.dockerfile` claim is load-bearing:
under fuego ADR-018, declared patterns are a parser's complete claim set, so
the scanner-emitted `<name>.dockerfile` files parse only because the module
claims that pattern.

### AD-4: The overview is a virtual page built in an Index hook

**Decision:** `graph.BuildOverviewHook` (a `core.IndexHook`) indexes all K8s and
Dockerfile pages, builds the graph and a per-namespace summary, and appends one
virtual `*core.Page` at `/` with `Layout: "diagram"`. It carries a server-rendered
`summary` in its envelope and the full `{nodes, edges}` as a single `graph-data`
node.

**Why:** Index hooks run during INDEX, after ROUTE has resolved real-page URLs
(so the summary and graph can link to `/kubernetes/{slug}/`) and before the
collision re-check, so the overview is validated like any other page. The summary
is rendered server-side (HTML, indexable, no-JS-friendly); the diagram is rendered
client-side from the graph JSON.

### AD-5: The diagram uses a deterministic, physics-free layout

**Decision:** `theme/layouts/diagram.html` computes node positions instead of
simulating them. Each namespace gets its own cell in a grid; within a cell, nodes
are stacked by role (Ingress → Service → Workload → Config/Secret) and wrapped.
Physics is disabled; a translucent labeled box is drawn behind each namespace.
All three views (Architecture / Traffic Flow / Dependencies) are **filtered
subsets of the same fixed positions**.

**Why:** Force-directed physics cannot *reliably* separate many environments — it
jitters on load, settles differently each time, and overlaps. Computed positions
render identically every load, never move, and never overlap. Sharing one fixed
layout across views means switching (and returning) is instant and stable, and
"Traffic Flow" becomes a clean per-environment subset instead of a global
hierarchical hairball.

### AD-6: Self-contained — nothing is written to the repo or cwd

**Decision:** The pack embeds its theme (`//go:embed all:theme`) and static
assets. `devops.Run` scans into an `os.MkdirTemp` content dir and removes it on
exit. No `config.yaml`, `theme/`, or `content/` is ever written to the scanned
repo or the working directory.

**Why:** The binary is self-contained — `go install` yields a working CLI, and
running it leaves no artifacts behind. (Earlier versions materialized the theme
and a generated `config.yaml` into the cwd; v0.3's `Pack.Theme` + `ConfigDefaults`
+ programmatic `engine.Build` removed all of that.)

### AD-7: Two entry points — the pack and the CLI/Run wrapper

**Decision:** fuego-devops exposes `devops.Pack()` (for composition over
already-scanned content) and `devops.Run(repoPath, Options)` / the
`fuego-devops` CLI (scan + build/serve). The CLI delegates to `Run`.

**Why:** The pack serves library consumers who already have content or want ADR-
style composition; `Run`/CLI serve the common case — point at a repo, get a site
— for CI pipelines and local use without writing Go.

## Project Structure

```
fuego-devops/
  devops.go                Pack() + Run(repoPath, Options): scan → engine.Build/Serve
  config-defaults.yaml     routes + resource_kind taxonomy (Pack.ConfigDefaults)
  cmd/fuego-devops/        CLI binary (flags → devops.Run)
  scanner/
    scanner.go             Repo walk, Kustomize/Helm render, dedup, content emit
  graph/
    graph.go               BuildOverviewHook (IndexHook) + edge construction
    summary.go             Per-namespace, workload-centric summary (deterministic)
  theme/
    base.html              HTML shell (dark theme, vis-network via CDN)
    layouts/
      default.html         Per-resource detail page
      diagram.html         Overview: summary + interactive diagram
    renderers/             Per-node-type HTML templates
```

The parsers live in fuego-formats (`docker`, `kubernetes` modules — see AD-8);
`scanner` and `graph` depend only on Fuego (`core`, `engine`), the two parser
modules' exported constants, and `gopkg.in/yaml.v3`. The root `devops` package
wires them into the pack and `Run`.

## Build flow

`fuego-devops ./my-repo build` does:

1. `devops.Run` applies option defaults and creates a temp content dir.
2. `scanner.Run(repoPath, contentDir)` — render Kustomize/Helm, dedup, emit
   `*.k8s` / `*.dockerfile` content files with frontmatter
   (`title`, `source_path`, `resource_kind`).
3. `engine.New()`; `eng.Use(devops.Pack())`.
4. `eng.Build`/`eng.Serve(ctx, BuildOptions{ContentDir, OutputDir, SiteName, BaseURL})`.
5. The temp content dir is removed on return.

Inside Fuego, the pack contributes the two fuego-formats parsers (so
`.k8s`/`.dockerfile`/`Dockerfile` are discovered as content), the theme, the
route/taxonomy defaults, and the Index hook that builds the overview.

## The graph hook

`BuildOverviewHook` constructs edges by reading node attributes (via the
fuego-formats modules' exported constants; the attribute names are those
modules' documented public API):

- **Ingress → Service** (`routes-to`): `k8s-ingress-rule.serviceName`
- **Service → Workload** (`selects`): `k8s-service-spec.selectorMap` matched against `k8s-pod-template-labels`
- **Workload → ConfigMap/Secret** (`env-from`): `k8s-env-ref.refKind` + `k8s-env-ref.refName`
- **Workload → ConfigMap/Secret/PVC** (`mounts`): `k8s-volume.refName` + `k8s-volume.volumeType`
- **Dockerfile → Workload** (`builds`): Dockerfile base image (envelope `images`, `[]any`) matches `k8s-container-spec.image` (tags stripped)

`summary.go` turns the graph into a per-namespace, workload-centric view: each
workload lists what **exposes** it (services + fronting ingresses), what it
**depends on** (configmaps/secrets/volumes), and what **builds** it (dockerfiles).
Resources not attached to a workload appear under the namespace's "other
resources". It is fully deterministic (namespaces, workloads, and refs sorted) —
covered by `graph/summary_test.go`.

## Node Type Reference

The authoritative contracts live in the fuego-formats modules'
[`kubernetes/schema.md`](https://github.com/gofuego/fuego-formats/blob/develop/kubernetes/schema.md)
and [`docker/schema.md`](https://github.com/gofuego/fuego-formats/blob/develop/docker/schema.md);
this table maps them to their graph usage here. Renderer templates in
`theme/renderers/` are named after these types.

### Kubernetes nodes (`kubernetes.Node*` constants)

| Node type | Emitted by | Key attributes | Used by graph |
|-----------|-----------|----------------|---------------|
| `k8s-resource-header` | All kinds | `kind`, `apiVersion`, `name`, `namespace` | Yes — graph node ID |
| `k8s-metadata` | All kinds | label/annotation key-value pairs | No |
| `k8s-replicas` | Workloads | `count` | No |
| `k8s-pod-template-labels` | Workloads | label key-value pairs | Yes — Service selector matching |
| `k8s-container-spec` | Workloads | `name`, `image`, `ports`, `limits`, `requests`, `volumeMounts` | Yes — Dockerfile image matching |
| `k8s-env-ref` | Workloads | `refKind`, `refName`, `container` | Yes — ConfigMap/Secret edges |
| `k8s-service-account-ref` | Workloads | `name` | No |
| `k8s-volume` | Workloads | `name`, `volumeType`, `refName` | Yes — volume mount edges |
| `k8s-service-spec` | Service | `serviceType`, `selector`, `selectorMap` | Yes — workload selection |
| `k8s-port-mapping` | Service | `port`, `targetPort`, `protocol` | No |
| `k8s-config-data` | ConfigMap | `key` (content is value) | No |
| `k8s-secret-data` | Secret | `keys` array (values redacted) | No |
| `k8s-ingress-rule` | Ingress | `host`, `path`, `pathType`, `serviceName`, `servicePort` | Yes — Service edges |
| `k8s-spec` | Unknown kinds | content is YAML | No |

### Dockerfile nodes (`docker.Node*` constants)

| Node type | Key attributes | Used by graph |
|-----------|----------------|---------------|
| `docker-stage` | `image`, `alias` | No (images go in the envelope, as `[]any`) |
| `docker-instruction` | `instruction`, `stage`, `copyFrom` | No |
| `docker-comment` | (content is text) | No |

## Common Tasks

### Add support for a new K8s kind
1. Add the kind in the fuego-formats `kubernetes` module (its schema.md is a
   contract — new node types are a release of that module), then bump this
   repo's dependency.
2. Emit nodes whose types match existing renderers (or add renderers here).
3. If the kind carries relationships (selectors, refs), emit nodes the graph hook can read.
4. Add edge logic in `buildGraph()` (and `summary.go`) if it participates in relationships.
5. Add a `theme/renderers/{type}.html` for any new node types.

### Add a new edge type to the graph
1. Ensure the parser emits a node carrying the relationship in its attributes.
2. Add edge logic in `buildGraph()` in `graph/graph.go` via `addEdge(from, to, type, label)` (it dedups).
3. Surface it in `summary.go` if it belongs in the per-namespace summary.
4. Add tests in `graph/graph_test.go`; optionally filter it in a `diagram.html` view.

### Teach the scanner a new source format (e.g. Terraform, Jsonnet)
1. Detect it in `scanner/scanner.go` — either a new templating tree (render then scan, like Kustomize/Helm) or a raw file type.
2. Emit content with the right extension and `resource_kind` frontmatter.
3. Add a parser in `parser/`, register it in `devops.Pack()`, and add a route in `config-defaults.yaml`.

### Change resource routing or add a taxonomy
Edit `config-defaults.yaml`. The site config / CLI options still win via deep-merge.

### Customize the diagram or a renderer
Edit `theme/layouts/diagram.html` or `theme/renderers/{type}.html`. The theme is
embedded, so rebuild the binary to pick up changes (`go build ./cmd/fuego-devops`).
A consumer can override any file by placing their own `theme/...` over the pack's.

## Testing

- `go test ./...` — unit tests for the scanner and graph/summary. (Parser
  tests live with the parsers, in the fuego-formats modules.)
- `scanner/scanner_test.go` covers detection, path flattening, and
  `TestKustomizeLeaves` (the base-vs-overlay distinction).
- `graph/summary_test.go` locks in the deterministic per-namespace summary.
- Manual end-to-end: `go build -o /tmp/fd ./cmd/fuego-devops && /tmp/fd ./some-repo build`.
- Render-aware scanning needs `kustomize` (and optionally `helm`) on `PATH`; the
  code degrades gracefully without them, but exercise both when changing the scanner.

## External Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/gofuego/fuego` | Meta-engine SSG — pipeline, routing, rendering, serving, pack API |
| `github.com/gofuego/fuego-formats/docker` | Dockerfile parser + `docker-*` node constants |
| `github.com/gofuego/fuego-formats/kubernetes` | K8s manifest parser + `k8s-*` node constants |
| `gopkg.in/yaml.v3` | YAML parsing (kustomization files, render-stream splitting in the scanner) |
| `kustomize` (binary, external) | Renders Kustomize overlays before scanning |
| `helm` (binary, external) | Renders Helm charts before scanning |

All other dependencies (cobra, doublestar, fsnotify, …) are transitive through fuego.

## Dependency note

`go.mod` pins a tagged Fuego release. While consuming an unreleased engine or
fuego-formats feature, pin a develop **pseudo-version** (resolvable in CI,
unlike a local-path replace) — currently the docker/kubernetes modules and the
formatkit replace-to-pseudo-version, until `docker/v0.1.0`,
`kubernetes/v0.1.0`, and `formatkit/v0.2.0` are tagged; then pin the tags and
drop the replace.

## What NOT to Do

- **Don't modify Fuego core for fuego-devops features.** Everything works through
  parsers, hooks, the pack, and templates. If you need a new engine capability,
  add it to Fuego's public API first.
- **Don't raw-scan Kustomize/Helm sources.** Render them and scan the output, or
  the namespaces and deduplication are wrong (AD-2).
- **Don't parse YAML in the graph hook.** Extract relationship data in the parsers
  into node attributes; the hook reads attributes, not raw content (AD-3).
- **Don't reintroduce force physics for the diagram.** Layout is computed and
  deterministic on purpose (AD-5).
- **Don't write files into the scanned repo or cwd.** The scanner uses a temp dir;
  the theme and config come from the pack (AD-6).
