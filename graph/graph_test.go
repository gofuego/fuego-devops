package graph

import (
	"testing"

	"github.com/FabioSol/fuego/core"
)

func makePage(typ, url string, envelope core.Envelope, nodes []core.Node) *core.Page {
	return &core.Page{
		Type:     typ,
		URL:      url,
		Envelope: envelope,
		Nodes:    nodes,
	}
}

func TestBuildGraphHook_IngressToService(t *testing.T) {
	pages := []*core.Page{
		makePage("k8s", "/kubernetes/ingress/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Ingress", "name": "web-ingress", "namespace": "prod"}},
			{Type: "ingress-rule", Attributes: map[string]any{"serviceName": "api-svc", "host": "example.com"}},
		}),
		makePage("k8s", "/kubernetes/svc/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Service", "name": "api-svc", "namespace": "prod"}},
			{Type: "service-spec", Attributes: map[string]any{"serviceType": "ClusterIP"}},
		}),
	}

	result, err := BuildGraphHook(pages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have original 2 pages + 1 virtual overview
	if len(result) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(result))
	}

	overview := result[2]
	if overview.Type != "diagram" {
		t.Fatalf("expected diagram type, got %q", overview.Type)
	}

	graphNodes, _ := overview.Nodes[0].Attributes["nodes"].([]GraphNode)
	graphEdges, _ := overview.Nodes[0].Attributes["edges"].([]GraphEdge)

	if len(graphNodes) != 2 {
		t.Fatalf("expected 2 graph nodes, got %d", len(graphNodes))
	}

	foundRoutesTo := false
	for _, e := range graphEdges {
		if e.Type == "routes-to" {
			foundRoutesTo = true
			if e.Label != "routes" {
				t.Errorf("expected label 'routes', got %q", e.Label)
			}
		}
	}
	if !foundRoutesTo {
		t.Error("expected routes-to edge from Ingress to Service")
	}
}

func TestBuildGraphHook_ServiceToDeployment(t *testing.T) {
	pages := []*core.Page{
		makePage("k8s", "/kubernetes/svc/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Service", "name": "api-svc", "namespace": "prod"}},
			{Type: "service-spec", Attributes: map[string]any{
				"serviceType": "ClusterIP",
				"selectorMap": map[string]any{"app": "api"},
			}},
		}),
		makePage("k8s", "/kubernetes/deploy/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Deployment", "name": "api", "namespace": "prod"}},
			{Type: "pod-template-labels", Attributes: map[string]any{"app": "api", "tier": "backend"}},
		}),
	}

	result, err := BuildGraphHook(pages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	overview := result[len(result)-1]
	edges, _ := overview.Nodes[0].Attributes["edges"].([]GraphEdge)

	foundSelects := false
	for _, e := range edges {
		if e.Type == "selects" {
			foundSelects = true
		}
	}
	if !foundSelects {
		t.Error("expected selects edge from Service to Deployment")
	}
}

func TestBuildGraphHook_EnvRef(t *testing.T) {
	pages := []*core.Page{
		makePage("k8s", "/kubernetes/deploy/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Deployment", "name": "api", "namespace": "prod"}},
			{Type: "env-ref", Attributes: map[string]any{"refKind": "ConfigMap", "refName": "app-config", "container": "api"}},
			{Type: "env-ref", Attributes: map[string]any{"refKind": "Secret", "refName": "db-creds", "container": "api"}},
		}),
		makePage("k8s", "/kubernetes/cm/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "ConfigMap", "name": "app-config", "namespace": "prod"}},
		}),
		makePage("k8s", "/kubernetes/secret/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Secret", "name": "db-creds", "namespace": "prod"}},
		}),
	}

	result, err := BuildGraphHook(pages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	overview := result[len(result)-1]
	edges, _ := overview.Nodes[0].Attributes["edges"].([]GraphEdge)

	envFromCount := 0
	for _, e := range edges {
		if e.Type == "env-from" {
			envFromCount++
		}
	}
	if envFromCount != 2 {
		t.Errorf("expected 2 env-from edges, got %d", envFromCount)
	}
}

func TestBuildGraphHook_VolumeRef(t *testing.T) {
	pages := []*core.Page{
		makePage("k8s", "/kubernetes/deploy/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Deployment", "name": "api", "namespace": "prod"}},
			{Type: "volume", Attributes: map[string]any{"name": "config-vol", "volumeType": "configMap", "refName": "app-config"}},
		}),
		makePage("k8s", "/kubernetes/cm/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "ConfigMap", "name": "app-config", "namespace": "prod"}},
		}),
	}

	result, err := BuildGraphHook(pages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	overview := result[len(result)-1]
	edges, _ := overview.Nodes[0].Attributes["edges"].([]GraphEdge)

	foundMounts := false
	for _, e := range edges {
		if e.Type == "mounts" {
			foundMounts = true
		}
	}
	if !foundMounts {
		t.Error("expected mounts edge from Deployment to ConfigMap")
	}
}

func TestBuildGraphHook_NoK8sPages(t *testing.T) {
	pages := []*core.Page{
		{Type: "taxonomy-term", URL: "/by-kind/k8s/"},
	}

	result, err := BuildGraphHook(pages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No k8s/dockerfile pages → no overview page added
	if len(result) != 1 {
		t.Errorf("expected 1 page unchanged, got %d", len(result))
	}
}

func TestBuildGraphHook_DockerfileImageMatch(t *testing.T) {
	pages := []*core.Page{
		makePage("dockerfile", "/dockerfiles/api/", core.Envelope{
			"images":      []string{"myapp"},
			"source_path": "services/api/Dockerfile",
		}, []core.Node{
			{Type: "stage", Attributes: map[string]any{"image": "myapp", "alias": ""}},
		}),
		makePage("k8s", "/kubernetes/deploy/", nil, []core.Node{
			{Type: "resource-header", Attributes: map[string]any{"kind": "Deployment", "name": "api", "namespace": "prod"}},
			{Type: "container-spec", Attributes: map[string]any{"image": "myapp:v1.2", "name": "api"}},
		}),
	}

	result, err := BuildGraphHook(pages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	overview := result[len(result)-1]
	edges, _ := overview.Nodes[0].Attributes["edges"].([]GraphEdge)

	foundBuilds := false
	for _, e := range edges {
		if e.Type == "builds" {
			foundBuilds = true
		}
	}
	if !foundBuilds {
		t.Error("expected builds edge from Dockerfile to Deployment")
	}
}

func TestMatchesSelector(t *testing.T) {
	tests := []struct {
		name     string
		selector map[string]any
		labels   map[string]any
		want     bool
	}{
		{"exact match", map[string]any{"app": "api"}, map[string]any{"app": "api"}, true},
		{"superset labels", map[string]any{"app": "api"}, map[string]any{"app": "api", "tier": "backend"}, true},
		{"missing label", map[string]any{"app": "api", "env": "prod"}, map[string]any{"app": "api"}, false},
		{"value mismatch", map[string]any{"app": "web"}, map[string]any{"app": "api"}, false},
		{"empty selector", map[string]any{}, map[string]any{"app": "api"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesSelector(tt.selector, tt.labels)
			if got != tt.want {
				t.Errorf("matchesSelector(%v, %v) = %v, want %v", tt.selector, tt.labels, got, tt.want)
			}
		})
	}
}
