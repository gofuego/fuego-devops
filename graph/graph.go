package graph

import (
	"fmt"
	"strings"

	"github.com/FabioSol/fuego/core"
)

// GraphNode represents a resource in the infrastructure graph.
type GraphNode struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	URL       string `json:"url"`
	Group     string `json:"group,omitempty"`
}

// GraphEdge represents a relationship between two resources.
type GraphEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
}

// Graph is the complete infrastructure graph.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// resourceKey uniquely identifies a K8s resource.
type resourceKey struct {
	namespace string
	kind      string
	name      string
}

// pageInfo holds extracted metadata for graph building.
type pageInfo struct {
	page      *core.Page
	key       resourceKey
	id        string
	nodeTypes map[string][]core.Node // nodes grouped by type
}

// BuildGraphHook is a BeforeRenderHook that builds an infrastructure graph
// from all parsed pages and injects a virtual overview page.
func BuildGraphHook(pages []*core.Page) ([]*core.Page, error) {
	infos := indexPages(pages)
	if len(infos) == 0 {
		return pages, nil
	}

	lookup := buildLookup(infos)
	g := buildGraph(infos, lookup)

	overviewPage := &core.Page{
		RelPath: "_virtual/overview",
		Envelope: core.Envelope{
			"title":  "Infrastructure Overview",
			"layout": "diagram",
		},
		Nodes: []core.Node{{
			Type: "graph-data",
			Attributes: map[string]any{
				"nodes": g.Nodes,
				"edges": g.Edges,
			},
		}},
		URL:    "/",
		Layout: "diagram",
		Type:   "diagram",
	}

	return append(pages, overviewPage), nil
}

// indexPages extracts structured metadata from each page.
func indexPages(pages []*core.Page) []pageInfo {
	var infos []pageInfo

	for _, p := range pages {
		if p.Type != "k8s" && p.Type != "dockerfile" {
			continue
		}

		info := pageInfo{
			page:      p,
			nodeTypes: groupNodesByType(p.Nodes),
		}

		if p.Type == "k8s" {
			headers := info.nodeTypes["resource-header"]
			if len(headers) == 0 {
				continue
			}
			h := headers[0]
			kind, _ := h.Attributes["kind"].(string)
			name, _ := h.Attributes["name"].(string)
			ns, _ := h.Attributes["namespace"].(string)
			info.key = resourceKey{namespace: ns, kind: kind, name: name}
			info.id = resourceID(ns, kind, name)
		} else {
			// Dockerfile
			sourcePath, _ := p.Envelope["source_path"].(string)
			if sourcePath == "" {
				sourcePath, _ = p.Envelope["title"].(string)
			}
			info.key = resourceKey{kind: "Dockerfile", name: sourcePath}
			info.id = "Dockerfile/" + sourcePath
		}

		infos = append(infos, info)
	}
	return infos
}

func groupNodesByType(nodes []core.Node) map[string][]core.Node {
	m := make(map[string][]core.Node)
	for _, n := range nodes {
		m[n.Type] = append(m[n.Type], n)
	}
	return m
}

func resourceID(ns, kind, name string) string {
	if ns != "" {
		return fmt.Sprintf("%s/%s/%s", ns, kind, name)
	}
	return fmt.Sprintf("%s/%s", kind, name)
}

// buildLookup creates maps for fast resource lookups.
func buildLookup(infos []pageInfo) map[string]*pageInfo {
	m := make(map[string]*pageInfo)
	for i := range infos {
		m[infos[i].id] = &infos[i]
	}
	return m
}

// findByKindName finds a resource by kind and name within a namespace.
func findByKindName(infos []pageInfo, ns, kind, name string) *pageInfo {
	// Try exact namespace match first
	for i := range infos {
		if infos[i].key.kind == kind && infos[i].key.name == name && infos[i].key.namespace == ns {
			return &infos[i]
		}
	}
	// Fall back to any namespace (for resources that may omit namespace)
	for i := range infos {
		if infos[i].key.kind == kind && infos[i].key.name == name {
			return &infos[i]
		}
	}
	return nil
}

// buildGraph constructs the full graph from indexed pages.
func buildGraph(infos []pageInfo, lookup map[string]*pageInfo) Graph {
	var g Graph

	// Build graph nodes
	for _, info := range infos {
		g.Nodes = append(g.Nodes, GraphNode{
			ID:        info.id,
			Label:     info.key.name,
			Kind:      info.key.kind,
			Namespace: info.key.namespace,
			URL:       info.page.URL,
			Group:     info.key.namespace,
		})
	}

	seen := make(map[string]bool) // dedup edges

	addEdge := func(from, to, edgeType, label string) {
		key := from + "|" + to + "|" + edgeType
		if from == to || seen[key] {
			return
		}
		seen[key] = true
		g.Edges = append(g.Edges, GraphEdge{
			From:  from,
			To:    to,
			Type:  edgeType,
			Label: label,
		})
	}

	for _, info := range infos {
		if info.page.Type != "k8s" {
			continue
		}

		// Ingress → Service
		for _, n := range info.nodeTypes["ingress-rule"] {
			svcName, _ := n.Attributes["serviceName"].(string)
			if svcName == "" {
				continue
			}
			if target := findByKindName(infos, info.key.namespace, "Service", svcName); target != nil {
				addEdge(info.id, target.id, "routes-to", "routes")
			}
		}

		// Service → Workload (selector matching)
		for _, n := range info.nodeTypes["service-spec"] {
			selectorMap, ok := n.Attributes["selectorMap"].(map[string]any)
			if !ok || len(selectorMap) == 0 {
				continue
			}
			for _, other := range infos {
				if other.key.namespace != info.key.namespace {
					continue
				}
				if !isWorkloadKind(other.key.kind) {
					continue
				}
				for _, pl := range other.nodeTypes["pod-template-labels"] {
					if matchesSelector(selectorMap, pl.Attributes) {
						addEdge(info.id, other.id, "selects", "selects")
					}
				}
			}
		}

		// Workload → ConfigMap/Secret (env refs)
		for _, n := range info.nodeTypes["env-ref"] {
			refKind, _ := n.Attributes["refKind"].(string)
			refName, _ := n.Attributes["refName"].(string)
			if refName == "" {
				continue
			}
			if target := findByKindName(infos, info.key.namespace, refKind, refName); target != nil {
				addEdge(info.id, target.id, "env-from", "env")
			}
		}

		// Workload → ConfigMap/Secret/PVC (volumes)
		for _, n := range info.nodeTypes["volume"] {
			refName, _ := n.Attributes["refName"].(string)
			volType, _ := n.Attributes["volumeType"].(string)
			if refName == "" {
				continue
			}
			targetKind := volumeTypeToKind(volType)
			if targetKind == "" {
				continue
			}
			if target := findByKindName(infos, info.key.namespace, targetKind, refName); target != nil {
				addEdge(info.id, target.id, "mounts", "volume")
			}
		}
	}

	// Dockerfile → Workload (image matching)
	for _, df := range infos {
		if df.key.kind != "Dockerfile" {
			continue
		}
		images, _ := df.page.Envelope["images"].([]string)
		if len(images) == 0 {
			continue
		}
		for _, wl := range infos {
			if !isWorkloadKind(wl.key.kind) {
				continue
			}
			for _, cs := range wl.nodeTypes["container-spec"] {
				containerImage, _ := cs.Attributes["image"].(string)
				if containerImage == "" {
					continue
				}
				for _, dfImage := range images {
					if imagesRelated(dfImage, containerImage) {
						addEdge(df.id, wl.id, "builds", "image")
					}
				}
			}
		}
	}

	return g
}

func isWorkloadKind(kind string) bool {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
		return true
	}
	return false
}

// matchesSelector checks if all selector entries exist in the labels.
func matchesSelector(selector, labels map[string]any) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		lv, ok := labels[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", v) != fmt.Sprintf("%v", lv) {
			return false
		}
	}
	return true
}

func volumeTypeToKind(volType string) string {
	switch volType {
	case "configMap":
		return "ConfigMap"
	case "secret":
		return "Secret"
	case "persistentVolumeClaim":
		return "PersistentVolumeClaim"
	}
	return ""
}

// imagesRelated checks if a Dockerfile base image and a container image share the same repo name.
func imagesRelated(dockerfileImage, containerImage string) bool {
	return stripTag(dockerfileImage) == stripTag(containerImage)
}

func stripTag(image string) string {
	// Remove tag or digest
	if i := strings.LastIndex(image, ":"); i > 0 {
		return image[:i]
	}
	if i := strings.LastIndex(image, "@"); i > 0 {
		return image[:i]
	}
	return image
}
