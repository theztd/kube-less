package scheduler

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"kube-less/internal/cri"
	"kube-less/internal/parser"
	"kube-less/internal/watcher"
)

// CRIRuntime is the subset of CRI operations the Scheduler needs.
// *cri.Client satisfies this interface. Extracted to allow testing with mocks.
type CRIRuntime interface {
	RunPodSandbox(ctx context.Context, config *runtimeapi.PodSandboxConfig) (string, error)
	StopPodSandbox(ctx context.Context, sandboxID string) error
	RemovePodSandbox(ctx context.Context, sandboxID string) error
	PodSandboxStatus(ctx context.Context, sandboxID string) (*runtimeapi.PodSandboxStatus, error)
	ListPodSandbox(ctx context.Context, filter *runtimeapi.PodSandboxFilter) ([]*runtimeapi.PodSandbox, error)
	ListContainers(ctx context.Context, filter *runtimeapi.ContainerFilter) ([]*runtimeapi.Container, error)
	CreateContainer(ctx context.Context, sandboxID string, config *runtimeapi.ContainerConfig, sbConfig *runtimeapi.PodSandboxConfig) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string, timeoutSecs int64) error
	RemoveContainer(ctx context.Context, containerID string) error
	PullImage(ctx context.Context, image string) error
	ImageStatus(ctx context.Context, image string) (*runtimeapi.Image, error)
}

// Scheduler orchestrates the lifecycle of pods based on manifests.
type Scheduler struct {
	store  *Store
	client CRIRuntime
	parser *parser.Parser
}

// NewScheduler creates a new Scheduler instance.
func NewScheduler(store *Store, client *cri.Client, p *parser.Parser) *Scheduler {
	return &Scheduler{
		store:  store,
		client: client,
		parser: p,
	}
}

// StartReconciliationLoop starts a periodic reconciliation loop.
// It compares desired state (Store) against actual CRI state and applies diffs.
func (s *Scheduler) StartReconciliationLoop(ctx context.Context, interval string) {
	d, err := time.ParseDuration(interval)
	if err != nil {
		log.Printf("Invalid sync_interval %q, defaulting to 10s: %v", interval, err)
		d = 10 * time.Second
	}

	go func() {
		ticker := time.NewTicker(d)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileAll(ctx)
			}
		}
	}()
}

// reconcileAll iterates over all workloads in the store and reconciles them.
func (s *Scheduler) reconcileAll(ctx context.Context) {
	sandboxes, err := s.client.ListPodSandbox(ctx, &runtimeapi.PodSandboxFilter{
		LabelSelector: map[string]string{cri.LabelManaged: "true"},
	})
	if err != nil {
		log.Printf("Reconcile: failed to list sandboxes: %v", err)
		return
	}

	// Build lookup: workload key → sandbox
	actualByKey := make(map[string]*runtimeapi.PodSandbox)
	for _, sb := range sandboxes {
		key := sb.Labels[cri.LabelNamespace] + "/" + sb.Labels[cri.LabelName]
		actualByKey[key] = sb
	}

	for _, ws := range s.store.GetWorkloads() {
		key := keyFunc(ws.Namespace, ws.Name)
		actual := actualByKey[key]

		actualSandboxID := ""
		actualHash := ""
		if actual != nil {
			actualSandboxID = actual.Id
			actualHash = actual.Annotations["kube-less/config-hash"]
		}

		action := compare(ws, actualSandboxID, actualHash)
		if action == ActionNone {
			continue
		}
		log.Printf("Reconcile: %s %s/%s", action, ws.Namespace, ws.Name)

		switch action {
		case ActionCreate:
			s.createWorkload(ctx, ws.Manifest)
		case ActionRecreate:
			s.deleteWorkload(ctx, *ws)
			s.createWorkload(ctx, ws.Manifest)
		case ActionDelete:
			s.deleteWorkload(ctx, *ws)
			s.store.DeleteWorkload(ws.Namespace, ws.Name)
		}
	}
}

// OnManifestEvent handles file system events from the watcher.
func (s *Scheduler) OnManifestEvent(event watcher.Event) {
	log.Printf("Scheduler received event: Type=%s, File=%s", event.Type, event.FilePath)

	if event.Type == watcher.EventAdded || event.Type == watcher.EventModified {
		s.handleUpdate(event.FilePath)
	} else if event.Type == watcher.EventDeleted {
		s.handleRemove(event.FilePath)
	}
}

// handleUpdate parses a manifest file and creates/recreates workloads as needed.
func (s *Scheduler) handleUpdate(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("handleUpdate: failed to read %s: %v", filePath, err)
		return
	}

	objects, err := s.parser.Parse(data)
	if err != nil {
		log.Printf("handleUpdate: failed to parse %s: %v", filePath, err)
		return
	}

	ctx := context.Background()
	var workloadKeys []string

	for _, obj := range objects {
		dep, ok := obj.(*appsv1.Deployment)
		if !ok {
			continue
		}

		key := keyFunc(dep.Namespace, dep.Name)
		workloadKeys = append(workloadKeys, key)

		newHash := ComputeConfigHash(dep)
		existing := s.store.GetWorkload(dep.Namespace, dep.Name)

		// No-op when already running with the same config
		if existing != nil && existing.ConfigHash == newHash && existing.Status == PodStatusRunning {
			log.Printf("handleUpdate: %s/%s unchanged, skipping", dep.Namespace, dep.Name)
			s.store.UpdateWorkload(dep.Namespace, dep.Name, dep)
			continue
		}

		// Tear down existing sandbox before creating a new one
		if existing != nil && existing.PodSandboxID != "" {
			log.Printf("handleUpdate: recreating %s/%s (hash changed)", dep.Namespace, dep.Name)
			s.deleteWorkload(ctx, *existing)
		}

		s.store.UpdateWorkload(dep.Namespace, dep.Name, dep)

		if err := s.createWorkload(ctx, dep); err != nil {
			log.Printf("handleUpdate: failed to create %s/%s: %v", dep.Namespace, dep.Name, err)
		}
	}

	if len(workloadKeys) > 0 {
		s.store.SetFileWorkloads(filePath, workloadKeys)
	}
}

// handleRemove stops and removes all workloads that were loaded from the given file.
func (s *Scheduler) handleRemove(filePath string) {
	keys := s.store.GetFileWorkloads(filePath)
	if len(keys) == 0 {
		log.Printf("handleRemove: no workloads known for %s", filePath)
		return
	}

	ctx := context.Background()
	for _, key := range keys {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			continue
		}
		namespace, name := parts[0], parts[1]

		ws := s.store.GetWorkload(namespace, name)
		if ws != nil {
			s.deleteWorkload(ctx, *ws)
		}
		s.store.DeleteWorkload(namespace, name)
		log.Printf("handleRemove: deleted %s", key)
	}

	s.store.DeleteFileWorkloads(filePath)
}

// createWorkload pulls images and starts a new pod sandbox + containers.
func (s *Scheduler) createWorkload(ctx context.Context, dep *appsv1.Deployment) error {
	// Pull images according to pull policy
	for _, c := range dep.Spec.Template.Spec.Containers {
		if err := s.pullImageWithPolicy(ctx, dep, c.Image); err != nil {
			return err
		}
	}

	sbConfig := cri.BuildPodSandboxConfig(dep)
	containerConfigs, err := cri.BuildContainerConfigs(dep, sbConfig, nil, nil)
	if err != nil {
		return err
	}

	sandboxID, err := s.client.RunPodSandbox(ctx, sbConfig)
	if err != nil {
		return err
	}

	// Fetch sandbox IP
	sandboxIP := ""
	if status, err := s.client.PodSandboxStatus(ctx, sandboxID); err == nil {
		if net := status.GetNetwork(); net != nil {
			sandboxIP = net.GetIp()
		}
	}

	var containerIDs []string
	for _, cfg := range containerConfigs {
		containerID, err := s.client.CreateContainer(ctx, sandboxID, cfg, sbConfig)
		if err != nil {
			s.cleanupSandbox(ctx, sandboxID, containerIDs)
			return err
		}
		if err := s.client.StartContainer(ctx, containerID); err != nil {
			s.cleanupSandbox(ctx, sandboxID, containerIDs)
			return err
		}
		containerIDs = append(containerIDs, containerID)
	}

	s.store.SetWorkloadRuntime(dep.Namespace, dep.Name, sandboxID, containerIDs, sandboxIP, ComputeConfigHash(dep))
	log.Printf("createWorkload: %s/%s started (sandbox=%s, ip=%s)", dep.Namespace, dep.Name, sandboxID, sandboxIP)
	return nil
}

// deleteWorkload stops and removes all containers and the sandbox for a workload.
func (s *Scheduler) deleteWorkload(ctx context.Context, ws WorkloadState) {
	for _, cid := range ws.ContainerIDs {
		if err := s.client.StopContainer(ctx, cid, 10); err != nil {
			log.Printf("deleteWorkload: failed to stop container %s: %v", cid, err)
		}
		if err := s.client.RemoveContainer(ctx, cid); err != nil {
			log.Printf("deleteWorkload: failed to remove container %s: %v", cid, err)
		}
	}
	if ws.PodSandboxID != "" {
		if err := s.client.StopPodSandbox(ctx, ws.PodSandboxID); err != nil {
			log.Printf("deleteWorkload: failed to stop sandbox %s: %v", ws.PodSandboxID, err)
		}
		if err := s.client.RemovePodSandbox(ctx, ws.PodSandboxID); err != nil {
			log.Printf("deleteWorkload: failed to remove sandbox %s: %v", ws.PodSandboxID, err)
		}
	}
}

// cleanupSandbox stops and removes a partially-created sandbox after a container error.
func (s *Scheduler) cleanupSandbox(ctx context.Context, sandboxID string, containerIDs []string) {
	for _, cid := range containerIDs {
		_ = s.client.StopContainer(ctx, cid, 5)
		_ = s.client.RemoveContainer(ctx, cid)
	}
	_ = s.client.StopPodSandbox(ctx, sandboxID)
	_ = s.client.RemovePodSandbox(ctx, sandboxID)
}

// pullImageWithPolicy pulls an image respecting the pull-policy annotation on the deployment.
func (s *Scheduler) pullImageWithPolicy(ctx context.Context, dep *appsv1.Deployment, image string) error {
	policy := cri.GetPullPolicy(dep)
	switch policy {
	case cri.PullPolicyNever:
		return nil
	case cri.PullPolicyAlways:
		return s.client.PullImage(ctx, image)
	default: // ifnotpresent
		img, err := s.client.ImageStatus(ctx, image)
		if err != nil || img == nil {
			return s.client.PullImage(ctx, image)
		}
		return nil
	}
}

// SyncStateFromCRI loads manifests and reconciles against the running CRI state at startup.
func (s *Scheduler) SyncStateFromCRI(ctx context.Context) error {
	sandboxes, err := s.client.ListPodSandbox(ctx, &runtimeapi.PodSandboxFilter{
		LabelSelector: map[string]string{cri.LabelManaged: "true"},
	})
	if err != nil {
		return err
	}

	// Build index of running kube-less sandboxes
	runningByKey := make(map[string]*runtimeapi.PodSandbox)
	for _, sb := range sandboxes {
		ns := sb.Labels[cri.LabelNamespace]
		name := sb.Labels[cri.LabelName]
		if ns != "" && name != "" {
			runningByKey[keyFunc(ns, name)] = sb
		}
	}

	// Reconcile desired state (Store) against actual CRI state
	for _, ws := range s.store.GetWorkloads() {
		key := keyFunc(ws.Namespace, ws.Name)
		sb, running := runningByKey[key]
		if running {
			// Update store with the actual sandbox ID
			s.store.UpdatePodStatus(ws.Namespace, ws.Name, sb.Id, TranslateCRIState(sb.State))
			delete(runningByKey, key)
		}
		// If manifest exists but no sandbox, reconcileAll will create it on the next tick
	}

	// Orphaned sandboxes (running but no manifest) → remove
	for key, sb := range runningByKey {
		log.Printf("SyncStateFromCRI: removing orphaned sandbox %s (%s)", sb.Id, key)
		if err := s.client.StopPodSandbox(ctx, sb.Id); err != nil {
			log.Printf("SyncStateFromCRI: failed to stop orphan %s: %v", sb.Id, err)
		}
		if err := s.client.RemovePodSandbox(ctx, sb.Id); err != nil {
			log.Printf("SyncStateFromCRI: failed to remove orphan %s: %v", sb.Id, err)
		}
	}

	return nil
}
