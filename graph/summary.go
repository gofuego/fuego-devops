package graph

import "sort"

// ResourceRef is a link to a resource in the summary.
type ResourceRef struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// WorkloadSummary describes one running workload and its relationships, the
// way a person reasons about a service: what runs, what exposes it, what it
// needs, and what builds it.
type WorkloadSummary struct {
	Name      string        `json:"name"`
	Kind      string        `json:"kind"`
	URL       string        `json:"url"`
	ExposedBy []ResourceRef `json:"exposedBy"` // services + ingresses fronting it
	DependsOn []ResourceRef `json:"dependsOn"` // configmaps / secrets / volumes
	BuiltBy   []ResourceRef `json:"builtBy"`   // dockerfiles that build its image
}

// NamespaceSummary groups a namespace's workloads and its other resources.
type NamespaceSummary struct {
	Name      string            `json:"name"`
	Workloads []WorkloadSummary `json:"workloads"`
	Others    []ResourceRef     `json:"others"` // resources not attached to a workload above
}

// buildSummary turns the graph into a per-namespace, workload-centric summary.
// It is deterministic: namespaces, workloads, and refs are all sorted.
func buildSummary(g Graph) []NamespaceSummary {
	byID := make(map[string]GraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	ref := func(id string) ResourceRef {
		n := byID[id]
		return ResourceRef{Name: n.Label, Kind: n.Kind, URL: n.URL}
	}

	// Index edges for the relationships we surface.
	dependsOn := map[string][]string{} // workload -> config/secret/volume targets
	selects := map[string][]string{}   // workload -> services selecting it
	routesTo := map[string][]string{}  // service -> ingresses routing to it
	builtBy := map[string][]string{}   // workload -> dockerfiles building it
	for _, e := range g.Edges {
		switch e.Type {
		case "env-from", "mounts":
			dependsOn[e.From] = appendUnique(dependsOn[e.From], e.To)
		case "selects":
			selects[e.To] = appendUnique(selects[e.To], e.From)
		case "routes-to":
			routesTo[e.To] = appendUnique(routesTo[e.To], e.From)
		case "builds":
			builtBy[e.To] = appendUnique(builtBy[e.To], e.From)
		}
	}

	// A resource is "covered" if it appears under a workload (so the Others
	// list only shows standalone resources).
	covered := map[string]bool{}

	nsWorkloads := map[string][]WorkloadSummary{}
	for _, n := range g.Nodes {
		if !isWorkloadKind(n.Kind) {
			continue
		}
		ws := WorkloadSummary{Name: n.Label, Kind: n.Kind, URL: n.URL}

		for _, dep := range dependsOn[n.ID] {
			ws.DependsOn = append(ws.DependsOn, ref(dep))
			covered[dep] = true
		}
		for _, svc := range selects[n.ID] {
			ws.ExposedBy = append(ws.ExposedBy, ref(svc))
			covered[svc] = true
			for _, ing := range routesTo[svc] {
				ws.ExposedBy = append(ws.ExposedBy, ref(ing))
				covered[ing] = true
			}
		}
		for _, df := range builtBy[n.ID] {
			ws.BuiltBy = append(ws.BuiltBy, ref(df))
			covered[df] = true
		}
		covered[n.ID] = true

		sortRefs(ws.ExposedBy)
		sortRefs(ws.DependsOn)
		sortRefs(ws.BuiltBy)
		nsWorkloads[n.Namespace] = append(nsWorkloads[n.Namespace], ws)
	}

	// Standalone resources per namespace (not attached to a workload above).
	nsOthers := map[string][]ResourceRef{}
	for _, n := range g.Nodes {
		if isWorkloadKind(n.Kind) || covered[n.ID] {
			continue
		}
		nsOthers[n.Namespace] = append(nsOthers[n.Namespace], ResourceRef{Name: n.Label, Kind: n.Kind, URL: n.URL})
	}

	// Assemble, sorted.
	nsSet := map[string]bool{}
	for ns := range nsWorkloads {
		nsSet[ns] = true
	}
	for ns := range nsOthers {
		nsSet[ns] = true
	}
	var names []string
	for ns := range nsSet {
		names = append(names, ns)
	}
	sort.Strings(names)

	var out []NamespaceSummary
	for _, ns := range names {
		wl := nsWorkloads[ns]
		sort.Slice(wl, func(i, j int) bool { return wl[i].Name < wl[j].Name })
		others := nsOthers[ns]
		sortRefs(others)
		out = append(out, NamespaceSummary{
			Name:      displayNamespace(ns),
			Workloads: wl,
			Others:    others,
		})
	}
	return out
}

func displayNamespace(ns string) string {
	if ns == "" {
		return "(cluster-scoped)"
	}
	return ns
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func sortRefs(refs []ResourceRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].Name < refs[j].Name
	})
}
