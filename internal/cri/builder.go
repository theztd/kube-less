package cri

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	LabelManaged   = "kube-less/managed"
	LabelNamespace = "kube-less/namespace"
	LabelName      = "kube-less/name"

	AnnotationPullPolicy = "kube-less.io/pull-policy"

	PullPolicyAlways       = "always"
	PullPolicyNever        = "never"
	PullPolicyIfNotPresent = "ifnotpresent"
)

// BuildPodSandboxConfig translates a Deployment into a CRI PodSandboxConfig.
// kube-less management labels are merged with any existing deployment labels.
// Port mappings from all containers are collected here (CRI port-mapping lives at sandbox level).
func BuildPodSandboxConfig(dep *appsv1.Deployment) *runtimeapi.PodSandboxConfig {
	labels := map[string]string{
		LabelManaged:   "true",
		LabelNamespace: dep.Namespace,
		LabelName:      dep.Name,
	}
	for k, v := range dep.Labels {
		labels[k] = v
	}

	var portMappings []*runtimeapi.PortMapping
	for _, c := range dep.Spec.Template.Spec.Containers {
		for _, p := range c.Ports {
			portMappings = append(portMappings, &runtimeapi.PortMapping{
				ContainerPort: p.ContainerPort,
				HostPort:      p.HostPort,
			})
		}
	}

	return &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      dep.Name,
			Namespace: dep.Namespace,
			Uid:       string(dep.UID),
			Attempt:   0,
		},
		Labels:       labels,
		PortMappings: portMappings,
		Linux: &runtimeapi.LinuxPodSandboxConfig{
			SecurityContext: &runtimeapi.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtimeapi.NamespaceOption{
					Network: runtimeapi.NamespaceMode_POD,
				},
			},
		},
	}
}

// BuildContainerConfigs translates the containers of a Deployment into CRI ContainerConfigs.
// Literal env vars and configMapKeyRef / secretKeyRef are resolved.
// Volume mounts (ConfigMap FS mount) are handled in Milestone C.
func BuildContainerConfigs(
	dep *appsv1.Deployment,
	sbConfig *runtimeapi.PodSandboxConfig,
	cms map[string]*v1.ConfigMap,
	secrets map[string]*v1.Secret,
) ([]*runtimeapi.ContainerConfig, error) {
	var configs []*runtimeapi.ContainerConfig

	for _, c := range dep.Spec.Template.Spec.Containers {
		envs, err := resolveEnvs(dep.Namespace, c.Env, cms, secrets)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", c.Name, err)
		}

		cfg := &runtimeapi.ContainerConfig{
			Metadata: &runtimeapi.ContainerMetadata{
				Name:    c.Name,
				Attempt: 0,
			},
			Image:   &runtimeapi.ImageSpec{Image: c.Image},
			Command: c.Command,
			Args:    c.Args,
			Envs:    envs,
			Linux: &runtimeapi.LinuxContainerConfig{
				SecurityContext: &runtimeapi.LinuxContainerSecurityContext{},
			},
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// GetPullPolicy returns the image pull policy from the deployment annotation.
// Defaults to "ifnotpresent".
func GetPullPolicy(dep *appsv1.Deployment) string {
	if ann, ok := dep.Annotations[AnnotationPullPolicy]; ok {
		switch strings.ToLower(ann) {
		case PullPolicyAlways, PullPolicyNever, PullPolicyIfNotPresent:
			return strings.ToLower(ann)
		}
	}
	return PullPolicyIfNotPresent
}

// resolveEnvs converts Kubernetes EnvVar list into CRI KeyValue pairs,
// resolving configMapKeyRef and secretKeyRef references.
func resolveEnvs(namespace string, envVars []v1.EnvVar, cms map[string]*v1.ConfigMap, secrets map[string]*v1.Secret) ([]*runtimeapi.KeyValue, error) {
	var result []*runtimeapi.KeyValue
	for _, e := range envVars {
		// Literal value (including empty string with no ValueFrom)
		if e.ValueFrom == nil {
			result = append(result, &runtimeapi.KeyValue{Key: e.Name, Value: e.Value})
			continue
		}

		if ref := e.ValueFrom.ConfigMapKeyRef; ref != nil {
			val, err := resolveConfigMapRef(namespace, e.Name, ref, cms)
			if err != nil {
				return nil, err
			}
			if val != nil {
				result = append(result, &runtimeapi.KeyValue{Key: e.Name, Value: *val})
			}
			continue
		}

		if ref := e.ValueFrom.SecretKeyRef; ref != nil {
			val, err := resolveSecretRef(namespace, e.Name, ref, secrets)
			if err != nil {
				return nil, err
			}
			if val != nil {
				result = append(result, &runtimeapi.KeyValue{Key: e.Name, Value: *val})
			}
			continue
		}
	}
	return result, nil
}

func resolveConfigMapRef(namespace, envName string, ref *v1.ConfigMapKeySelector, cms map[string]*v1.ConfigMap) (*string, error) {
	optional := ref.Optional != nil && *ref.Optional
	if cms == nil {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("env %s: configMapKeyRef %s/%s not available (no configmaps loaded)", envName, namespace, ref.Name)
	}
	cm, ok := cms[namespace+"/"+ref.Name]
	if !ok {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("env %s: configMap %s/%s not found", envName, namespace, ref.Name)
	}
	val, ok := cm.Data[ref.Key]
	if !ok {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("env %s: key %q not found in configMap %s/%s", envName, ref.Key, namespace, ref.Name)
	}
	return &val, nil
}

func resolveSecretRef(namespace, envName string, ref *v1.SecretKeySelector, secrets map[string]*v1.Secret) (*string, error) {
	optional := ref.Optional != nil && *ref.Optional
	if secrets == nil {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("env %s: secretKeyRef %s/%s not available (no secrets loaded)", envName, namespace, ref.Name)
	}
	secret, ok := secrets[namespace+"/"+ref.Name]
	if !ok {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("env %s: secret %s/%s not found", envName, namespace, ref.Name)
	}
	val, ok := secret.Data[ref.Key]
	if !ok {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("env %s: key %q not found in secret %s/%s", envName, ref.Key, namespace, ref.Name)
	}
	s := string(val)
	return &s, nil
}
