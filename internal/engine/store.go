package engine

import (
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

// WorkloadState represents the state of a single workload (e.g., a Deployment).
type WorkloadState struct {
	// Name is the name of the workload (e.g., "nginx").
	Name string `json:"name"`
	// Namespace is the namespace of the workload (e.g., "default").
	Namespace string `json:"namespace"`
	// Manifest is the desired state (parsed from YAML).
	Manifest *appsv1.Deployment `json:"-"` // Don't serialize the full manifest
	// PodSandboxID is the ID of the CRI sandbox if it exists.
	PodSandboxID string `json:"pod_sandbox_id,omitempty"`
	// Status is the current status of the workload.
	Status PodStatus `json:"status"`
	// LastUpdated is the timestamp of the last state change.
	LastUpdated time.Time `json:"last_updated"`
}

// Store is a thread-safe in-memory store for tracking workload states.
type Store struct {
	mu        sync.RWMutex
	workloads map[string]*WorkloadState // Key is "namespace/name"
}

// NewStore creates a new Store instance.
func NewStore() *Store {
	return &Store{
		workloads: make(map[string]*WorkloadState),
	}
}

// UpdateWorkload updates the state of a workload.
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

// GetWorkloads returns a snapshot of all workloads.
func (s *Store) GetWorkloads() []*WorkloadState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*WorkloadState
	for _, w := range s.workloads {
		// Return a copy to avoid race conditions if the caller modifies it (though pointers are still shared)
		// For display purposes, this shallow copy of the struct is fine.
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

func keyFunc(namespace, name string) string {
	return namespace + "/" + name
}

// Helper: Translate CRI State to PodStatus
func TranslateCRIState(state runtimeapi.PodSandboxState) PodStatus {
	switch state {
	case runtimeapi.PodSandboxState_SANDBOX_READY:
		return PodStatusRunning
	case runtimeapi.PodSandboxState_SANDBOX_NOTREADY:
		return PodStatusPending // Or Stopped, depending on context
	default:
		return PodStatusUnknown
	}
}
