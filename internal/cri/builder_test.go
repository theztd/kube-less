package cri

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func baseDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "default",
			Labels:    map[string]string{"app": "web"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
}

// ── BuildPodSandboxConfig ─────────────────────────────────────────────────────

func TestBuildPodSandboxConfig_Metadata(t *testing.T) {
	dep := baseDeployment()
	cfg := BuildPodSandboxConfig(dep)

	if cfg.Metadata.Name != "web" {
		t.Errorf("expected name=web, got %s", cfg.Metadata.Name)
	}
	if cfg.Metadata.Namespace != "default" {
		t.Errorf("expected namespace=default, got %s", cfg.Metadata.Namespace)
	}
}

func TestBuildPodSandboxConfig_ManagementLabels(t *testing.T) {
	dep := baseDeployment()
	cfg := BuildPodSandboxConfig(dep)

	if cfg.Labels[LabelManaged] != "true" {
		t.Errorf("missing label %s=true", LabelManaged)
	}
	if cfg.Labels[LabelName] != "web" {
		t.Errorf("missing label %s=web", LabelName)
	}
	if cfg.Labels[LabelNamespace] != "default" {
		t.Errorf("missing label %s=default", LabelNamespace)
	}
	// Original deployment labels preserved
	if cfg.Labels["app"] != "web" {
		t.Error("deployment label 'app' should be preserved in sandbox config")
	}
}

// ── BuildContainerConfigs ─────────────────────────────────────────────────────

func TestBuildContainerConfigs_LiteralEnv(t *testing.T) {
	dep := baseDeployment()
	dep.Spec.Template.Spec.Containers[0].Env = []v1.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "EMPTY", Value: ""},
	}

	sbCfg := BuildPodSandboxConfig(dep)
	cfgs, err := BuildContainerConfigs(dep, sbCfg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("expected 1 container config, got %d", len(cfgs))
	}
	envs := cfgs[0].Envs
	if len(envs) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(envs))
	}
	if envs[0].Key != "FOO" || envs[0].Value != "bar" {
		t.Errorf("unexpected env[0]: %+v", envs[0])
	}
}

func TestBuildContainerConfigs_ConfigMapRef(t *testing.T) {
	dep := baseDeployment()
	dep.Spec.Template.Spec.Containers[0].Env = []v1.EnvVar{
		{
			Name: "DB_HOST",
			ValueFrom: &v1.EnvVarSource{
				ConfigMapKeyRef: &v1.ConfigMapKeySelector{
					LocalObjectReference: v1.LocalObjectReference{Name: "app-config"},
					Key:                  "db_host",
				},
			},
		},
	}

	cms := map[string]*v1.ConfigMap{
		"default/app-config": {
			Data: map[string]string{"db_host": "postgres:5432"},
		},
	}

	sbCfg := BuildPodSandboxConfig(dep)
	cfgs, err := BuildContainerConfigs(dep, sbCfg, cms, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfgs[0].Envs[0].Value != "postgres:5432" {
		t.Errorf("expected postgres:5432, got %s", cfgs[0].Envs[0].Value)
	}
}

func TestBuildContainerConfigs_SecretRef(t *testing.T) {
	dep := baseDeployment()
	dep.Spec.Template.Spec.Containers[0].Env = []v1.EnvVar{
		{
			Name: "API_KEY",
			ValueFrom: &v1.EnvVarSource{
				SecretKeyRef: &v1.SecretKeySelector{
					LocalObjectReference: v1.LocalObjectReference{Name: "app-secret"},
					Key:                  "api_key",
				},
			},
		},
	}

	secrets := map[string]*v1.Secret{
		"default/app-secret": {
			Data: map[string][]byte{"api_key": []byte("supersecret")},
		},
	}

	sbCfg := BuildPodSandboxConfig(dep)
	cfgs, err := BuildContainerConfigs(dep, sbCfg, nil, secrets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfgs[0].Envs[0].Value != "supersecret" {
		t.Errorf("expected supersecret, got %s", cfgs[0].Envs[0].Value)
	}
}

func TestBuildContainerConfigs_MissingConfigMap_Error(t *testing.T) {
	dep := baseDeployment()
	dep.Spec.Template.Spec.Containers[0].Env = []v1.EnvVar{
		{
			Name: "DB_HOST",
			ValueFrom: &v1.EnvVarSource{
				ConfigMapKeyRef: &v1.ConfigMapKeySelector{
					LocalObjectReference: v1.LocalObjectReference{Name: "missing-cm"},
					Key:                  "db_host",
				},
			},
		},
	}

	sbCfg := BuildPodSandboxConfig(dep)
	_, err := BuildContainerConfigs(dep, sbCfg, nil, nil)
	if err == nil {
		t.Error("expected error for missing configmap")
	}
}

func TestBuildContainerConfigs_OptionalMissingRef_Skipped(t *testing.T) {
	dep := baseDeployment()
	dep.Spec.Template.Spec.Containers[0].Env = []v1.EnvVar{
		{
			Name: "OPTIONAL_VAR",
			ValueFrom: &v1.EnvVarSource{
				ConfigMapKeyRef: &v1.ConfigMapKeySelector{
					LocalObjectReference: v1.LocalObjectReference{Name: "missing-cm"},
					Key:                  "key",
					Optional:             ptr.To(true),
				},
			},
		},
	}

	sbCfg := BuildPodSandboxConfig(dep)
	cfgs, err := BuildContainerConfigs(dep, sbCfg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error for optional ref: %v", err)
	}
	if len(cfgs[0].Envs) != 0 {
		t.Errorf("expected 0 envs (optional skipped), got %d", len(cfgs[0].Envs))
	}
}

func TestBuildPodSandboxConfig_PortMappings(t *testing.T) {
	dep := baseDeployment()
	dep.Spec.Template.Spec.Containers[0].Ports = []v1.ContainerPort{
		{ContainerPort: 80, HostPort: 8080},
	}

	// Port mappings live at the sandbox level in CRI
	sbCfg := BuildPodSandboxConfig(dep)
	if len(sbCfg.PortMappings) != 1 {
		t.Fatalf("expected 1 port mapping on sandbox config, got %d", len(sbCfg.PortMappings))
	}
	pm := sbCfg.PortMappings[0]
	if pm.ContainerPort != 80 || pm.HostPort != 8080 {
		t.Errorf("unexpected port mapping: %+v", pm)
	}
}

// ── GetPullPolicy ─────────────────────────────────────────────────────────────

func TestGetPullPolicy_Default(t *testing.T) {
	dep := baseDeployment()
	if p := GetPullPolicy(dep); p != PullPolicyIfNotPresent {
		t.Errorf("expected ifnotpresent, got %s", p)
	}
}

func TestGetPullPolicy_Annotation(t *testing.T) {
	dep := baseDeployment()
	dep.Annotations = map[string]string{AnnotationPullPolicy: "Always"}
	if p := GetPullPolicy(dep); p != PullPolicyAlways {
		t.Errorf("expected always, got %s", p)
	}
}

func TestGetPullPolicy_InvalidAnnotation(t *testing.T) {
	dep := baseDeployment()
	dep.Annotations = map[string]string{AnnotationPullPolicy: "garbage"}
	if p := GetPullPolicy(dep); p != PullPolicyIfNotPresent {
		t.Errorf("invalid annotation should fall back to ifnotpresent, got %s", p)
	}
}
