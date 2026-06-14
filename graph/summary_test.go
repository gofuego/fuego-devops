package graph

import (
	"reflect"
	"testing"
)

func TestBuildSummary(t *testing.T) {
	g := Graph{
		Nodes: []GraphNode{
			{ID: "prod/Ingress/web", Label: "web", Kind: "Ingress", Namespace: "prod", URL: "/kubernetes/web/"},
			{ID: "prod/Service/api", Label: "api", Kind: "Service", Namespace: "prod", URL: "/kubernetes/api-svc/"},
			{ID: "prod/Deployment/api", Label: "api", Kind: "Deployment", Namespace: "prod", URL: "/kubernetes/api/"},
			{ID: "prod/ConfigMap/cfg", Label: "cfg", Kind: "ConfigMap", Namespace: "prod", URL: "/kubernetes/cfg/"},
			{ID: "prod/Secret/db", Label: "db", Kind: "Secret", Namespace: "prod", URL: "/kubernetes/db/"},
			{ID: "prod/ConfigMap/orphan", Label: "orphan", Kind: "ConfigMap", Namespace: "prod", URL: "/kubernetes/orphan/"},
		},
		Edges: []GraphEdge{
			{From: "prod/Ingress/web", To: "prod/Service/api", Type: "routes-to"},
			{From: "prod/Service/api", To: "prod/Deployment/api", Type: "selects"},
			{From: "prod/Deployment/api", To: "prod/ConfigMap/cfg", Type: "env-from"},
			{From: "prod/Deployment/api", To: "prod/Secret/db", Type: "mounts"},
		},
	}

	s := buildSummary(g)
	if len(s) != 1 || s[0].Name != "prod" {
		t.Fatalf("expected one 'prod' namespace, got %+v", s)
	}
	ns := s[0]
	if len(ns.Workloads) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(ns.Workloads))
	}
	w := ns.Workloads[0]
	if w.Name != "api" || w.Kind != "Deployment" {
		t.Errorf("workload = %+v", w)
	}
	// exposed by the service AND the ingress fronting it.
	exposed := refNames(w.ExposedBy)
	if !reflect.DeepEqual(exposed, []string{"web", "api"}) && !sameSet(exposed, []string{"web", "api"}) {
		t.Errorf("exposedBy = %v, want service api + ingress web", exposed)
	}
	// depends on the configmap and the secret.
	if !sameSet(refNames(w.DependsOn), []string{"cfg", "db"}) {
		t.Errorf("dependsOn = %v, want cfg + db", refNames(w.DependsOn))
	}
	// the unattached configmap shows under "others"; covered ones do not.
	if !sameSet(refNames(ns.Others), []string{"orphan"}) {
		t.Errorf("others = %v, want only the orphan configmap", refNames(ns.Others))
	}
}

func TestBuildSummaryDeterministic(t *testing.T) {
	g := Graph{Nodes: []GraphNode{
		{ID: "b/Deployment/z", Label: "z", Kind: "Deployment", Namespace: "b"},
		{ID: "a/Deployment/y", Label: "y", Kind: "Deployment", Namespace: "a"},
		{ID: "a/Deployment/x", Label: "x", Kind: "Deployment", Namespace: "a"},
	}}
	first := buildSummary(g)
	for i := 0; i < 5; i++ {
		if !reflect.DeepEqual(buildSummary(g), first) {
			t.Fatal("buildSummary is not deterministic")
		}
	}
	// namespaces sorted, workloads sorted within namespace.
	if first[0].Name != "a" || first[1].Name != "b" {
		t.Errorf("namespaces not sorted: %s, %s", first[0].Name, first[1].Name)
	}
	if first[0].Workloads[0].Name != "x" || first[0].Workloads[1].Name != "y" {
		t.Error("workloads not sorted within namespace")
	}
}

func refNames(refs []ResourceRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
