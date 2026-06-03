package parser

import (
	"testing"
)

func TestKubernetesParser_Type(t *testing.T) {
	p := Kubernetes()
	if p.Type() != "k8s" {
		t.Errorf("expected type 'k8s', got %q", p.Type())
	}
}

func TestKubernetesParser_Deployment(t *testing.T) {
	raw := []byte(`---
title: API Server
source_path: k8s/deployment.yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-server
  namespace: production
  labels:
    app: api
spec:
  replicas: 3
  template:
    metadata:
      labels:
        app: api
        tier: backend
    spec:
      containers:
        - name: api
          image: myapp:v1.2
          ports:
            - containerPort: 8080
          resources:
            limits:
              cpu: "500m"
              memory: "256Mi"
`)

	p := Kubernetes()
	env, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if env["title"] != "API Server" {
		t.Errorf("expected title from frontmatter, got %v", env["title"])
	}

	header := nodes[0]
	if header.Type != "resource-header" {
		t.Errorf("expected resource-header, got %q", header.Type)
	}
	if header.Attributes["kind"] != "Deployment" {
		t.Errorf("expected kind Deployment, got %v", header.Attributes["kind"])
	}
	if header.Attributes["name"] != "api-server" {
		t.Errorf("expected name api-server, got %v", header.Attributes["name"])
	}

	// Check pod-template-labels node
	foundPodLabels := false
	for _, n := range nodes {
		if n.Type == "pod-template-labels" {
			foundPodLabels = true
			if n.Attributes["app"] != "api" {
				t.Errorf("expected pod label app=api, got %v", n.Attributes["app"])
			}
			if n.Attributes["tier"] != "backend" {
				t.Errorf("expected pod label tier=backend, got %v", n.Attributes["tier"])
			}
		}
	}
	if !foundPodLabels {
		t.Error("expected pod-template-labels node")
	}
}

func TestKubernetesParser_Service(t *testing.T) {
	raw := []byte(`---
title: API Service
---
apiVersion: v1
kind: Service
metadata:
  name: api-svc
spec:
  type: LoadBalancer
  selector:
    app: api
  ports:
    - port: 80
      targetPort: 8080
`)

	p := Kubernetes()
	_, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundServiceSpec := false
	foundPortMapping := false
	for _, n := range nodes {
		if n.Type == "service-spec" {
			foundServiceSpec = true
			if n.Attributes["serviceType"] != "LoadBalancer" {
				t.Errorf("expected LoadBalancer, got %v", n.Attributes["serviceType"])
			}
			selectorMap, ok := n.Attributes["selectorMap"].(map[string]any)
			if !ok {
				t.Error("expected selectorMap as map[string]any")
			} else if selectorMap["app"] != "api" {
				t.Errorf("expected selectorMap app=api, got %v", selectorMap["app"])
			}
		}
		if n.Type == "port-mapping" {
			foundPortMapping = true
		}
	}
	if !foundServiceSpec {
		t.Error("expected service-spec node")
	}
	if !foundPortMapping {
		t.Error("expected port-mapping node")
	}
}

func TestKubernetesParser_ConfigMap(t *testing.T) {
	raw := []byte(`---
title: App Config
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
data:
  DATABASE_URL: "postgres://localhost/mydb"
  LOG_LEVEL: "info"
`)

	p := Kubernetes()
	_, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configNodes := 0
	for _, n := range nodes {
		if n.Type == "config-data" {
			configNodes++
		}
	}
	if configNodes != 2 {
		t.Errorf("expected 2 config-data nodes, got %d", configNodes)
	}
}

func TestKubernetesParser_EnvFrom(t *testing.T) {
	raw := []byte(`---
title: Worker
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
spec:
  template:
    spec:
      containers:
        - name: worker
          image: worker:latest
          envFrom:
            - configMapRef:
                name: app-config
            - secretRef:
                name: db-credentials
`)

	p := Kubernetes()
	_, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envRefs := map[string]string{}
	for _, n := range nodes {
		if n.Type == "env-ref" {
			kind, _ := n.Attributes["refKind"].(string)
			name, _ := n.Attributes["refName"].(string)
			envRefs[name] = kind
		}
	}

	if envRefs["app-config"] != "ConfigMap" {
		t.Errorf("expected ConfigMap ref for app-config, got %v", envRefs["app-config"])
	}
	if envRefs["db-credentials"] != "Secret" {
		t.Errorf("expected Secret ref for db-credentials, got %v", envRefs["db-credentials"])
	}
}

func TestKubernetesParser_ValueFrom(t *testing.T) {
	raw := []byte(`---
title: API
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    spec:
      containers:
        - name: api
          image: api:latest
          env:
            - name: DB_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: db-secret
                  key: password
            - name: APP_MODE
              valueFrom:
                configMapKeyRef:
                  name: app-config
                  key: mode
            - name: SIMPLE
              value: "plain"
`)

	p := Kubernetes()
	_, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envRefs := map[string]string{}
	for _, n := range nodes {
		if n.Type == "env-ref" {
			kind, _ := n.Attributes["refKind"].(string)
			name, _ := n.Attributes["refName"].(string)
			envRefs[name] = kind
		}
	}

	if envRefs["db-secret"] != "Secret" {
		t.Errorf("expected Secret ref for db-secret, got %v", envRefs["db-secret"])
	}
	if envRefs["app-config"] != "ConfigMap" {
		t.Errorf("expected ConfigMap ref for app-config, got %v", envRefs["app-config"])
	}
}

func TestKubernetesParser_VolumeRefName(t *testing.T) {
	raw := []byte(`---
title: API
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    spec:
      containers:
        - name: api
          image: api:latest
      volumes:
        - name: config-vol
          configMap:
            name: app-config
        - name: secret-vol
          secret:
            secretName: tls-cert
        - name: data-vol
          persistentVolumeClaim:
            claimName: data-pvc
        - name: tmp
          emptyDir: {}
`)

	p := Kubernetes()
	_, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	volumes := map[string]struct{ volType, refName string }{}
	for _, n := range nodes {
		if n.Type == "volume" {
			name, _ := n.Attributes["name"].(string)
			vt, _ := n.Attributes["volumeType"].(string)
			rn, _ := n.Attributes["refName"].(string)
			volumes[name] = struct{ volType, refName string }{vt, rn}
		}
	}

	if v := volumes["config-vol"]; v.volType != "configMap" || v.refName != "app-config" {
		t.Errorf("config-vol: expected configMap/app-config, got %s/%s", v.volType, v.refName)
	}
	if v := volumes["secret-vol"]; v.volType != "secret" || v.refName != "tls-cert" {
		t.Errorf("secret-vol: expected secret/tls-cert, got %s/%s", v.volType, v.refName)
	}
	if v := volumes["data-vol"]; v.volType != "persistentVolumeClaim" || v.refName != "data-pvc" {
		t.Errorf("data-vol: expected persistentVolumeClaim/data-pvc, got %s/%s", v.volType, v.refName)
	}
	if v := volumes["tmp"]; v.volType != "emptyDir" || v.refName != "" {
		t.Errorf("tmp: expected emptyDir with no refName, got %s/%s", v.volType, v.refName)
	}
}
