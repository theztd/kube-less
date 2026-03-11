package scheduler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
	Name         string             `json:"name"`
	Namespace    string             `json:"namespace"`
	Manifest     *appsv1.Deployment `json:"-"`
	PodSandboxID string             `json:"pod_sandbox_id,omitempty"`
	ContainerIDs []string           `json:"container_ids,omitempty"`
	SandboxIP    string             `json:"sandbox_ip,omitempty"`
	ConfigHash   string             `json:"config_hash,omitempty"`
	StartedAt    time.Time          `json:"started_at,omitempty"`
	Status       PodStatus          `json:"status"`
	LastUpdated  time.Time          `json:"last_updated"`
}

// Store is a thread-safe in-memory store for workload desired/actual state.
type Store struct {
	mu              sync.RWMutex
	workloads       map[string]*WorkloadState
	fileToWorkloads map[string][]string // filePath → []"namespace/name"
}

// NewStore creates a new Store instance.
func NewStore() *Store {
	return &Store{
		workloads:       make(map[string]*WorkloadState),
		fileToWorkloads: make(map[string][]string),
	}
}

// UpdateWorkload upserts the desired manifest for a workload.
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

// UpdatePodStatus updates the runtime status of a workload based on CRI data.
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

// GetWorkload returns a copy of the WorkloadState for a given workload, or nil.
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

// DeleteWorkload removes a workload from the store.
func (s *Store) DeleteWorkload(namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workloads, keyFunc(namespace, name))
}

// SetFileWorkloads records which workload keys were loaded from a manifest file.
func (s *Store) SetFileWorkloads(filePath string, keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileToWorkloads[filePath] = keys
}

// GetFileWorkloads returns the workload keys ("namespace/name") associated with a file.
func (s *Store) GetFileWorkloads(filePath string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := s.fileToWorkloads[filePath]
	result := make([]string, len(keys))
	copy(result, keys)
	return result
}

// DeleteFileWorkloads removes the file→workload mapping.
func (s *Store) DeleteFileWorkloads(filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fileToWorkloads, filePath)
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

// ComputeConfigHash returns a SHA256 hex digest of the deployment's PodTemplateSpec.
// Used to detect configuration changes that require a pod restart.
func ComputeConfigHash(dep *appsv1.Deployment) string {
	data, err := json.Marshal(dep.Spec.Template.Spec)
	if err != nil {
		// Fallback: hash the deployment name+namespace (always deterministic)
		data = []byte(fmt.Sprintf("%s/%s", dep.Namespace, dep.Name))
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
