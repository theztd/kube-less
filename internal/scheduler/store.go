package scheduler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// PodStatus represents the status of a single Pod managed by kube-less.
type PodStatus string

const (
	PodStatusUnknown PodStatus = "Unknown"
	PodStatusPending PodStatus = "Pending"
	PodStatusRunning PodStatus = "Running"
	PodStatusStopped PodStatus = "Stopped"
)

// WorkloadState represents the desired and actual state of a single workload.
type WorkloadState struct {
	Name            string             `json:"name"`
	Namespace       string             `json:"namespace"`
	Manifest        *appsv1.Deployment `json:"-"`
	PodSandboxID    string             `json:"pod_sandbox_id,omitempty"`
	ContainerIDs    []string           `json:"container_ids,omitempty"`
	SandboxIP       string             `json:"sandbox_ip,omitempty"`
	ConfigHash      string             `json:"config_hash,omitempty"`
	StartedAt       time.Time          `json:"started_at,omitempty"`
	Status          PodStatus          `json:"status"`
	Ready           bool               `json:"ready"`
	ReadyContainers int                `json:"ready_containers"`
	LastUpdated     time.Time          `json:"last_updated"`
}

// Store is a thread-safe in-memory store for workload desired/actual state,
// ConfigMaps and Secrets.
type Store struct {
	mu              sync.RWMutex
	workloads       map[string]*WorkloadState
	fileToWorkloads map[string][]string // filePath → []"namespace/name"
	configMaps      map[string]*v1.ConfigMap
	secrets         map[string]*v1.Secret
	fileToCMs       map[string][]string // filePath → []"namespace/name"
	fileToSecrets   map[string][]string // filePath → []"namespace/name"
}

// NewStore creates a new Store instance.
func NewStore() *Store {
	return &Store{
		workloads:       make(map[string]*WorkloadState),
		fileToWorkloads: make(map[string][]string),
		configMaps:      make(map[string]*v1.ConfigMap),
		secrets:         make(map[string]*v1.Secret),
		fileToCMs:       make(map[string][]string),
		fileToSecrets:   make(map[string][]string),
	}
}

// ── Workload ──────────────────────────────────────────────────────────────────

// UpdateWorkload upserts the desired manifest and recomputes ConfigHash
// to include current ConfigMap/Secret values referenced by the deployment.
func (s *Store) UpdateWorkload(namespace, name string, manifest *appsv1.Deployment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := keyFunc(namespace, name)
	state, exists := s.workloads[key]
	if !exists {
		state = &WorkloadState{
			Name:      name,
			Namespace: namespace,
			Status:    PodStatusPending,
		}
		s.workloads[key] = state
	}
	state.Manifest = manifest
	state.ConfigHash = computeEffectiveHash(manifest, s.configMaps, s.secrets)
	state.LastUpdated = time.Now()
}

// SetWorkloadRuntime records CRI runtime data after a successful pod creation.
func (s *Store) SetWorkloadRuntime(namespace, name, sandboxID string, containerIDs []string, sandboxIP, configHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := keyFunc(namespace, name)
	state, exists := s.workloads[key]
	if !exists {
		return
	}
	state.PodSandboxID = sandboxID
	state.ContainerIDs = containerIDs
	state.SandboxIP = sandboxIP
	state.ConfigHash = configHash
	state.StartedAt = time.Now()
	state.Status = PodStatusRunning
	state.LastUpdated = time.Now()
}

// UpdateRuntimeStatus updates CRI runtime data without touching ConfigHash.
// Used by SyncStateFromCRI to record actual sandbox/container IDs at startup.
func (s *Store) UpdateRuntimeStatus(namespace, name, sandboxID string, containerIDs []string, sandboxIP string, status PodStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := keyFunc(namespace, name)
	state, exists := s.workloads[key]
	if !exists {
		return
	}
	state.PodSandboxID = sandboxID
	state.ContainerIDs = containerIDs
	state.SandboxIP = sandboxIP
	state.Status = status
	state.LastUpdated = time.Now()
}

// UpdatePodStatus updates the runtime status of a workload.
func (s *Store) UpdatePodStatus(namespace, name, sandboxID string, status PodStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := keyFunc(namespace, name)
	if state, exists := s.workloads[key]; exists {
		state.PodSandboxID = sandboxID
		state.Status = status
		state.LastUpdated = time.Now()
	}
}

// GetWorkload returns a copy of the WorkloadState, or nil if not found.
func (s *Store) GetWorkload(namespace, name string) *WorkloadState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if state, exists := s.workloads[keyFunc(namespace, name)]; exists {
		copy := *state
		return &copy
	}
	return nil
}

// GetWorkloads returns a snapshot of all workloads.
func (s *Store) GetWorkloads() []*WorkloadState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*WorkloadState, 0, len(s.workloads))
	for _, w := range s.workloads {
		copy := *w
		result = append(result, &copy)
	}
	return result
}

// SetWorkloadReady updates the readiness state of a workload.
func (s *Store) SetWorkloadReady(namespace, name string, ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := keyFunc(namespace, name)
	if state, exists := s.workloads[key]; exists {
		state.Ready = ready
		if ready {
			state.ReadyContainers = len(state.ContainerIDs)
		} else {
			state.ReadyContainers = 0
		}
		state.LastUpdated = time.Now()
	}
}

// DeleteWorkload removes a workload from the store.
func (s *Store) DeleteWorkload(namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workloads, keyFunc(namespace, name))
}

// ── File → workload mappings ──────────────────────────────────────────────────

func (s *Store) SetFileWorkloads(filePath string, keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileToWorkloads[filePath] = keys
}

func (s *Store) GetFileWorkloads(filePath string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := s.fileToWorkloads[filePath]
	result := make([]string, len(keys))
	copy(result, keys)
	return result
}

func (s *Store) DeleteFileWorkloads(filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fileToWorkloads, filePath)
}

// ── ConfigMap ─────────────────────────────────────────────────────────────────

// UpdateConfigMap upserts a ConfigMap and recomputes hashes for all workloads
// in the same namespace so reconcileAll can detect the change.
func (s *Store) UpdateConfigMap(namespace, name string, cm *v1.ConfigMap) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configMaps[keyFunc(namespace, name)] = cm
	s.recomputeWorkloadHashes()
}

// GetConfigMap returns the ConfigMap or nil.
func (s *Store) GetConfigMap(namespace, name string) *v1.ConfigMap {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configMaps[keyFunc(namespace, name)]
}

// DeleteConfigMap removes a ConfigMap and recomputes affected workload hashes.
func (s *Store) DeleteConfigMap(namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.configMaps, keyFunc(namespace, name))
	s.recomputeWorkloadHashes()
}

// GetAllConfigMaps returns a shallow copy of all ConfigMaps keyed by "namespace/name".
func (s *Store) GetAllConfigMaps() map[string]*v1.ConfigMap {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*v1.ConfigMap, len(s.configMaps))
	for k, v := range s.configMaps {
		result[k] = v
	}
	return result
}

// ── Secret ────────────────────────────────────────────────────────────────────

// UpdateSecret upserts a Secret and recomputes hashes for all workloads.
func (s *Store) UpdateSecret(namespace, name string, secret *v1.Secret) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[keyFunc(namespace, name)] = secret
	s.recomputeWorkloadHashes()
}

// GetSecret returns the Secret or nil.
func (s *Store) GetSecret(namespace, name string) *v1.Secret {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secrets[keyFunc(namespace, name)]
}

// DeleteSecret removes a Secret and recomputes affected workload hashes.
func (s *Store) DeleteSecret(namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, keyFunc(namespace, name))
	s.recomputeWorkloadHashes()
}

// GetAllSecrets returns a shallow copy of all Secrets keyed by "namespace/name".
func (s *Store) GetAllSecrets() map[string]*v1.Secret {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*v1.Secret, len(s.secrets))
	for k, v := range s.secrets {
		result[k] = v
	}
	return result
}

// ── File → CM/Secret mappings ─────────────────────────────────────────────────

func (s *Store) SetFileCMs(filePath string, keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileToCMs[filePath] = keys
}

func (s *Store) GetFileCMs(filePath string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := s.fileToCMs[filePath]
	result := make([]string, len(keys))
	copy(result, keys)
	return result
}

func (s *Store) DeleteFileCMs(filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fileToCMs, filePath)
}

func (s *Store) SetFileSecrets(filePath string, keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileToSecrets[filePath] = keys
}

func (s *Store) GetFileSecrets(filePath string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := s.fileToSecrets[filePath]
	result := make([]string, len(keys))
	copy(result, keys)
	return result
}

func (s *Store) DeleteFileSecrets(filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fileToSecrets, filePath)
}

// ── Internals ─────────────────────────────────────────────────────────────────

// recomputeWorkloadHashes recomputes ConfigHash for all workloads using current
// CM/Secret values. Must be called under the write lock.
func (s *Store) recomputeWorkloadHashes() {
	for _, ws := range s.workloads {
		if ws.Manifest != nil {
			ws.ConfigHash = computeEffectiveHash(ws.Manifest, s.configMaps, s.secrets)
		}
	}
}

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}

// TranslateCRIState maps a CRI sandbox state to our internal PodStatus.
func TranslateCRIState(state runtimeapi.PodSandboxState) PodStatus {
	switch state {
	case runtimeapi.PodSandboxState_SANDBOX_READY:
		return PodStatusRunning
	case runtimeapi.PodSandboxState_SANDBOX_NOTREADY:
		return PodStatusPending
	default:
		return PodStatusUnknown
	}
}

// ComputeConfigHash returns the effective hash for a deployment including
// referenced ConfigMap/Secret values. Exported for use in createWorkload.
func ComputeConfigHash(dep *appsv1.Deployment) string {
	return computeEffectiveHash(dep, nil, nil)
}

// computeEffectiveHash hashes the PodTemplateSpec together with the data of all
// ConfigMaps and Secrets referenced by this deployment (env refs + volume refs).
func computeEffectiveHash(dep *appsv1.Deployment, cms map[string]*v1.ConfigMap, secrets map[string]*v1.Secret) string {
	type hashInput struct {
		Spec    v1.PodSpec
		CMData  map[string]map[string]string
		SecData map[string]map[string]string
	}
	input := hashInput{
		Spec:    dep.Spec.Template.Spec,
		CMData:  collectReferencedCMData(dep, cms),
		SecData: collectReferencedSecretData(dep, secrets),
	}
	data, err := json.Marshal(input)
	if err != nil {
		data = []byte(fmt.Sprintf("%s/%s", dep.Namespace, dep.Name))
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func collectReferencedCMData(dep *appsv1.Deployment, cms map[string]*v1.ConfigMap) map[string]map[string]string {
	if cms == nil {
		return nil
	}
	result := make(map[string]map[string]string)
	ns := dep.Namespace

	for _, c := range dep.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.ConfigMapKeyRef != nil {
				cmKey := keyFunc(ns, e.ValueFrom.ConfigMapKeyRef.Name)
				if cm, ok := cms[cmKey]; ok {
					result[cmKey] = cm.Data
				}
			}
		}
	}
	for _, vol := range dep.Spec.Template.Spec.Volumes {
		if vol.ConfigMap != nil {
			cmKey := keyFunc(ns, vol.ConfigMap.Name)
			if cm, ok := cms[cmKey]; ok {
				result[cmKey] = cm.Data
			}
		}
	}
	return result
}

func collectReferencedSecretData(dep *appsv1.Deployment, secrets map[string]*v1.Secret) map[string]map[string]string {
	if secrets == nil {
		return nil
	}
	result := make(map[string]map[string]string)
	ns := dep.Namespace

	for _, c := range dep.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				secKey := keyFunc(ns, e.ValueFrom.SecretKeyRef.Name)
				if secret, ok := secrets[secKey]; ok {
					strData := make(map[string]string, len(secret.Data))
					for k, v := range secret.Data {
						strData[k] = string(v)
					}
					result[secKey] = strData
				}
			}
		}
	}
	return result
}
