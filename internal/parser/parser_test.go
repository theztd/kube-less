package parser

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

var validDeployment = []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:latest
`)

var validConfigMap = []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: default
data:
  key: value
`)

var validSecret = []byte(`
apiVersion: v1
kind: Secret
metadata:
  name: app-secret
  namespace: default
type: Opaque
data:
  password: cGFzc3dvcmQ=
`)

var unsupportedKind = []byte(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-sa
  namespace: default
`)

var multiDocument = []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:latest
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: default
data:
  key: value
`)

func TestParse_ValidDeployment(t *testing.T) {
	p := NewParser()
	objs, err := p.Parse(validDeployment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	dep, ok := objs[0].(*appsv1.Deployment)
	if !ok {
		t.Fatalf("expected *appsv1.Deployment, got %T", objs[0])
	}
	if dep.Name != "nginx" {
		t.Errorf("expected name %q, got %q", "nginx", dep.Name)
	}
	if dep.Namespace != "default" {
		t.Errorf("expected namespace %q, got %q", "default", dep.Namespace)
	}
}

func TestParse_ValidConfigMap(t *testing.T) {
	p := NewParser()
	objs, err := p.Parse(validConfigMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	cm, ok := objs[0].(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("expected *corev1.ConfigMap, got %T", objs[0])
	}
	if cm.Name != "app-config" {
		t.Errorf("expected name %q, got %q", "app-config", cm.Name)
	}
}

func TestParse_ValidSecret(t *testing.T) {
	p := NewParser()
	objs, err := p.Parse(validSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	if _, ok := objs[0].(*corev1.Secret); !ok {
		t.Fatalf("expected *corev1.Secret, got %T", objs[0])
	}
}

func TestParse_MultiDocument(t *testing.T) {
	p := NewParser()
	objs, err := p.Parse(multiDocument)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}
	if _, ok := objs[0].(*appsv1.Deployment); !ok {
		t.Errorf("expected first object to be *appsv1.Deployment, got %T", objs[0])
	}
	if _, ok := objs[1].(*corev1.ConfigMap); !ok {
		t.Errorf("expected second object to be *corev1.ConfigMap, got %T", objs[1])
	}
}

func TestParse_UnsupportedKind_SkippedWithoutError(t *testing.T) {
	p := NewParser()
	objs, err := p.Parse(unsupportedKind)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 0 {
		t.Errorf("expected 0 objects for unsupported kind, got %d", len(objs))
	}
}

func TestParse_InvalidYAML_ReturnsError(t *testing.T) {
	p := NewParser()
	_, err := p.Parse([]byte("{ invalid yaml: ["))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestParse_EmptyInput_ReturnsEmpty(t *testing.T) {
	p := NewParser()
	objs, err := p.Parse([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 0 {
		t.Errorf("expected 0 objects for empty input, got %d", len(objs))
	}
}

func TestParse_DeploymentContainerImage(t *testing.T) {
	p := NewParser()
	objs, err := p.Parse(validDeployment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dep := objs[0].(*appsv1.Deployment)
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	if containers[0].Image != "nginx:latest" {
		t.Errorf("expected image %q, got %q", "nginx:latest", containers[0].Image)
	}
}
