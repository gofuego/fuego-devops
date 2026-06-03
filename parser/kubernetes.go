package parser

import (
	"fmt"
	"strings"

	"github.com/FabioSol/fuego/core"
	"gopkg.in/yaml.v3"
)

// Kubernetes returns a parser for Kubernetes manifest files (.k8s).
// These are YAML manifests wrapped by the scanner with frontmatter.
func Kubernetes() core.Parser {
	return core.WithYAMLFrontmatter("k8s", parseKubernetes)
}

func parseKubernetes(payload []byte, meta core.Envelope) ([]core.Node, error) {
	var manifest map[string]any
	if err := yaml.Unmarshal(payload, &manifest); err != nil {
		return nil, fmt.Errorf("parsing kubernetes YAML: %w", err)
	}
	if manifest == nil {
		return nil, nil
	}

	kind, _ := manifest["kind"].(string)
	apiVersion, _ := manifest["apiVersion"].(string)

	var nodes []core.Node

	// Resource header
	name, namespace := extractMetadata(manifest)
	nodes = append(nodes, core.Node{
		Type: "resource-header",
		Attributes: map[string]any{
			"kind":       kind,
			"apiVersion": apiVersion,
			"name":       name,
			"namespace":  namespace,
		},
	})

	// Labels and annotations
	if md, ok := manifest["metadata"].(map[string]any); ok {
		if labels, ok := md["labels"].(map[string]any); ok && len(labels) > 0 {
			nodes = append(nodes, core.Node{
				Type:       "metadata",
				Content:    "Labels",
				Attributes: labels,
			})
		}
		if annotations, ok := md["annotations"].(map[string]any); ok && len(annotations) > 0 {
			nodes = append(nodes, core.Node{
				Type:       "metadata",
				Content:    "Annotations",
				Attributes: annotations,
			})
		}
	}

	// Kind-specific parsing
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
		nodes = append(nodes, parseWorkload(manifest)...)
	case "Service":
		nodes = append(nodes, parseService(manifest)...)
	case "ConfigMap":
		nodes = append(nodes, parseConfigMap(manifest)...)
	case "Secret":
		nodes = append(nodes, core.Node{
			Type:    "secret-data",
			Content: "Secret data keys are hidden",
			Attributes: map[string]any{
				"keys": extractSecretKeys(manifest),
			},
		})
	case "Ingress":
		nodes = append(nodes, parseIngress(manifest)...)
	default:
		// Generic: render the spec as YAML
		if spec, ok := manifest["spec"]; ok {
			specYAML, _ := yaml.Marshal(spec)
			nodes = append(nodes, core.Node{
				Type:    "spec",
				Content: string(specYAML),
			})
		}
	}

	return nodes, nil
}

func extractMetadata(manifest map[string]any) (string, string) {
	md, _ := manifest["metadata"].(map[string]any)
	if md == nil {
		return "", ""
	}
	name, _ := md["name"].(string)
	namespace, _ := md["namespace"].(string)
	return name, namespace
}

func parseWorkload(manifest map[string]any) []core.Node {
	var nodes []core.Node

	spec := dig(manifest, "spec")
	if spec == nil {
		return nodes
	}

	// Replicas
	if replicas, ok := spec["replicas"]; ok {
		nodes = append(nodes, core.Node{
			Type:       "replicas",
			Content:    fmt.Sprintf("%v", replicas),
			Attributes: map[string]any{"count": replicas},
		})
	}

	// Pod template labels (used for Service selector matching)
	templateLabels := dig(spec, "template", "metadata")
	if templateLabels != nil {
		if labels, ok := templateLabels["labels"].(map[string]any); ok && len(labels) > 0 {
			nodes = append(nodes, core.Node{
				Type:       "pod-template-labels",
				Attributes: labels,
			})
		}
	}

	// Containers
	podSpec := dig(spec, "template", "spec")
	if podSpec == nil {
		return nodes
	}

	containers, _ := podSpec["containers"].([]any)
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		nodes = append(nodes, parseContainer(cm)...)
	}

	// Init containers
	initContainers, _ := podSpec["initContainers"].([]any)
	for _, c := range initContainers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		containerNodes := parseContainer(cm)
		if len(containerNodes) > 0 {
			containerNodes[0].Attributes["init"] = true
		}
		nodes = append(nodes, containerNodes...)
	}

	// ServiceAccountName
	if sa, ok := podSpec["serviceAccountName"].(string); ok && sa != "" {
		nodes = append(nodes, core.Node{
			Type:       "service-account-ref",
			Attributes: map[string]any{"name": sa},
		})
	}

	// Volumes
	volumes, _ := podSpec["volumes"].([]any)
	for _, v := range volumes {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		name, _ := vm["name"].(string)
		volType, refName := identifyVolumeSource(vm)
		attrs := map[string]any{
			"name":       name,
			"volumeType": volType,
		}
		if refName != "" {
			attrs["refName"] = refName
		}
		nodes = append(nodes, core.Node{
			Type:       "volume",
			Attributes: attrs,
		})
	}

	return nodes
}

func parseContainer(cm map[string]any) []core.Node {
	name, _ := cm["name"].(string)
	image, _ := cm["image"].(string)

	attrs := map[string]any{
		"name":  name,
		"image": image,
	}

	var extraNodes []core.Node

	// Ports
	if ports, ok := cm["ports"].([]any); ok {
		var portStrs []string
		for _, p := range ports {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			cp, _ := pm["containerPort"]
			proto, _ := pm["protocol"]
			if proto == nil {
				proto = "TCP"
			}
			portStrs = append(portStrs, fmt.Sprintf("%v/%v", cp, proto))
		}
		attrs["ports"] = strings.Join(portStrs, ", ")
	}

	// Resource limits/requests
	if resources, ok := cm["resources"].(map[string]any); ok {
		if limits, ok := resources["limits"].(map[string]any); ok {
			attrs["limits"] = formatResources(limits)
		}
		if requests, ok := resources["requests"].(map[string]any); ok {
			attrs["requests"] = formatResources(requests)
		}
	}

	// Env vars: count + valueFrom references
	if env, ok := cm["env"].([]any); ok {
		attrs["envCount"] = len(env)
		for _, e := range env {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			vf, ok := em["valueFrom"].(map[string]any)
			if !ok {
				continue
			}
			if cmkr, ok := vf["configMapKeyRef"].(map[string]any); ok {
				refName, _ := cmkr["name"].(string)
				if refName != "" {
					extraNodes = append(extraNodes, core.Node{
						Type: "env-ref",
						Attributes: map[string]any{
							"refKind":   "ConfigMap",
							"refName":   refName,
							"container": name,
						},
					})
				}
			}
			if skr, ok := vf["secretKeyRef"].(map[string]any); ok {
				refName, _ := skr["name"].(string)
				if refName != "" {
					extraNodes = append(extraNodes, core.Node{
						Type: "env-ref",
						Attributes: map[string]any{
							"refKind":   "Secret",
							"refName":   refName,
							"container": name,
						},
					})
				}
			}
		}
	}

	// envFrom references
	if envFrom, ok := cm["envFrom"].([]any); ok {
		for _, ef := range envFrom {
			efm, ok := ef.(map[string]any)
			if !ok {
				continue
			}
			if cmRef, ok := efm["configMapRef"].(map[string]any); ok {
				refName, _ := cmRef["name"].(string)
				if refName != "" {
					extraNodes = append(extraNodes, core.Node{
						Type: "env-ref",
						Attributes: map[string]any{
							"refKind":   "ConfigMap",
							"refName":   refName,
							"container": name,
						},
					})
				}
			}
			if secRef, ok := efm["secretRef"].(map[string]any); ok {
				refName, _ := secRef["name"].(string)
				if refName != "" {
					extraNodes = append(extraNodes, core.Node{
						Type: "env-ref",
						Attributes: map[string]any{
							"refKind":   "Secret",
							"refName":   refName,
							"container": name,
						},
					})
				}
			}
		}
	}

	// Volume mounts
	if mounts, ok := cm["volumeMounts"].([]any); ok {
		var mountList []any
		for _, m := range mounts {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			mountName, _ := mm["name"].(string)
			mountPath, _ := mm["mountPath"].(string)
			mountList = append(mountList, map[string]any{"name": mountName, "mountPath": mountPath})
		}
		if len(mountList) > 0 {
			attrs["volumeMounts"] = mountList
		}
	}

	nodes := []core.Node{{
		Type:       "container-spec",
		Content:    image,
		Attributes: attrs,
	}}
	return append(nodes, extraNodes...)
}

func parseService(manifest map[string]any) []core.Node {
	spec := dig(manifest, "spec")
	if spec == nil {
		return nil
	}

	svcType, _ := spec["type"].(string)
	if svcType == "" {
		svcType = "ClusterIP"
	}

	attrs := map[string]any{
		"serviceType": svcType,
	}

	if selector, ok := spec["selector"].(map[string]any); ok {
		var parts []string
		for k, v := range selector {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		attrs["selector"] = strings.Join(parts, ", ")
		attrs["selectorMap"] = selector
	}

	var nodes []core.Node
	nodes = append(nodes, core.Node{
		Type:       "service-spec",
		Attributes: attrs,
	})

	if ports, ok := spec["ports"].([]any); ok {
		for _, p := range ports {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			port, _ := pm["port"]
			targetPort, _ := pm["targetPort"]
			proto, _ := pm["protocol"]
			if proto == nil {
				proto = "TCP"
			}
			nodes = append(nodes, core.Node{
				Type: "port-mapping",
				Attributes: map[string]any{
					"port":       port,
					"targetPort": targetPort,
					"protocol":   proto,
				},
			})
		}
	}

	return nodes
}

func parseConfigMap(manifest map[string]any) []core.Node {
	var nodes []core.Node
	if data, ok := manifest["data"].(map[string]any); ok {
		for k, v := range data {
			nodes = append(nodes, core.Node{
				Type:    "config-data",
				Content: fmt.Sprintf("%v", v),
				Attributes: map[string]any{
					"key": k,
				},
			})
		}
	}
	return nodes
}

func parseIngress(manifest map[string]any) []core.Node {
	var nodes []core.Node
	spec := dig(manifest, "spec")
	if spec == nil {
		return nodes
	}

	rules, _ := spec["rules"].([]any)
	for _, r := range rules {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		host, _ := rm["host"].(string)
		http, _ := rm["http"].(map[string]any)
		paths, _ := http["paths"].([]any)

		for _, p := range paths {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			path, _ := pm["path"].(string)
			pathType, _ := pm["pathType"].(string)

			backend := dig(pm, "backend", "service")
			var svcName string
			var svcPort any
			if backend != nil {
				svcName, _ = backend["name"].(string)
				port := dig(backend, "port")
				if port != nil {
					if n, ok := port["number"]; ok {
						svcPort = n
					} else if n, ok := port["name"]; ok {
						svcPort = n
					}
				}
			}

			nodes = append(nodes, core.Node{
				Type: "ingress-rule",
				Attributes: map[string]any{
					"host":        host,
					"path":        path,
					"pathType":    pathType,
					"serviceName": svcName,
					"servicePort": svcPort,
				},
			})
		}
	}

	return nodes
}

func extractSecretKeys(manifest map[string]any) []string {
	var keys []string
	if data, ok := manifest["data"].(map[string]any); ok {
		for k := range data {
			keys = append(keys, k)
		}
	}
	if data, ok := manifest["stringData"].(map[string]any); ok {
		for k := range data {
			keys = append(keys, k)
		}
	}
	return keys
}

func formatResources(r map[string]any) string {
	var parts []string
	for k, v := range r {
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
	}
	return strings.Join(parts, ", ")
}

// identifyVolumeSource returns the volume type and the name of the referenced resource (if any).
func identifyVolumeSource(vm map[string]any) (string, string) {
	// Types that reference a named resource
	if cm, ok := vm["configMap"].(map[string]any); ok {
		refName, _ := cm["name"].(string)
		return "configMap", refName
	}
	if sec, ok := vm["secret"].(map[string]any); ok {
		refName, _ := sec["secretName"].(string)
		return "secret", refName
	}
	if pvc, ok := vm["persistentVolumeClaim"].(map[string]any); ok {
		refName, _ := pvc["claimName"].(string)
		return "persistentVolumeClaim", refName
	}

	// Types without a named reference
	otherTypes := []string{
		"emptyDir", "hostPath", "nfs", "awsElasticBlockStore",
		"gcePersistentDisk", "azureDisk", "projected", "downwardAPI",
	}
	for _, t := range otherTypes {
		if _, ok := vm[t]; ok {
			return t, ""
		}
	}
	return "unknown", ""
}

// dig navigates nested maps by key path.
func dig(m map[string]any, keys ...string) map[string]any {
	current := m
	for _, k := range keys {
		next, ok := current[k].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}
