package cri

import (
	"fmt"
	"os"
	"path/filepath"
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
// Resolves literal envs, configMapKeyRef, secretKeyRef, and ConfigMap volume mounts.
// For volume mounts, ConfigMap data is written to <dataDir>/configmaps/<ns>/<cm-name>/
// and mounted read-only into the container.
// Pass dataDir="" to skip volume mount processing (e.g. in tests without disk access).
func BuildContainerConfigs(
	dep *appsv1.Deployment,
	sbConfig *runtimeapi.PodSandboxConfig,
	cms map[string]*v1.ConfigMap,
	secrets map[string]*v1.Secret,
	dataDir string,
) ([]*runtimeapi.ContainerConfig, error) {
	// Pre-process pod-level volumes that back ConfigMaps:
	// write files to the host and record the hostPath per volume name.
	cmHostPaths, err := prepareConfigMapVolumes(dep, cms, dataDir)
	if err != nil {
		return nil, err
	}

	var configs []*runtimeapi.ContainerConfig
	for _, c := range dep.Spec.Template.Spec.Containers {
		envs, err := resolveEnvs(dep.Namespace, c.Env, cms, secrets)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", c.Name, err)
		}

		var mounts []*runtimeapi.Mount
		for _, vm := range c.VolumeMounts {
			if hostPath, ok := cmHostPaths[vm.Name]; ok {
				mounts = append(mounts, &runtimeapi.Mount{
					HostPath:      hostPath,
					ContainerPath: vm.MountPath,
					Readonly:      true,
				})
			}
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
			Mounts:  mounts,
			Linux: &runtimeapi.LinuxContainerConfig{
				SecurityContext: &runtimeapi.LinuxContainerSecurityContext{},
			},
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// prepareConfigMapVolumes writes ConfigMap data to disk for all CM-backed volumes
// and returns a map from volume name to host directory path.
func prepareConfigMapVolumes(dep *appsv1.Deployment, cms map[string]*v1.ConfigMap, dataDir string) (map[string]string, error) {
	hostPaths := make(map[string]string)
	if dataDir == "" {
		return hostPaths, nil
	}

	for _, vol := range dep.Spec.Template.Spec.Volumes {
		if vol.ConfigMap == nil {
			continue
		}
		optional := vol.ConfigMap.Optional != nil && *vol.ConfigMap.Optional
		cmKey := dep.Namespace + "/" + vol.ConfigMap.Name

		var cmData map[string]string
		if cms != nil {
			if cm, ok := cms[cmKey]; ok {
				cmData = cm.Data
			}
		}
		if cmData == nil {
			if optional {
				continue
			}
			return nil, fmt.Errorf("volume %q: configMap %q not found in store", vol.Name, vol.ConfigMap.Name)
		}

		hostPath := filepath.Join(dataDir, "configmaps", dep.Namespace, vol.ConfigMap.Name)
		if err := writeConfigMapFiles(hostPath, cmData); err != nil {
			return nil, fmt.Errorf("volume %q: failed to write configmap files: %w", vol.Name, err)
		}
		hostPaths[vol.Name] = hostPath
	}
	return hostPaths, nil
}

// writeConfigMapFiles writes each key of the ConfigMap as a file under hostPath.
// The directory is created with 0755 and files with 0644.
func writeConfigMapFiles(hostPath string, data map[string]string) error {
	if err := os.MkdirAll(hostPath, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", hostPath, err)
	}
	for key, value := range data {
		filePath := filepath.Join(hostPath, key)
		if err := os.WriteFile(filePath, []byte(value), 0644); err != nil {
			return fmt.Errorf("write %s: %w", filePath, err)
		}
	}
	return nil
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
