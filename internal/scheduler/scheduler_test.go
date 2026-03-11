package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"kube-less/internal/parser"
)

// ── mock CRI runtime ──────────────────────────────────────────────────────────

type mockCRI struct {
	// canned responses
	sandboxes  []*runtimeapi.PodSandbox
	containers []*runtimeapi.Container
	sandboxIP  string
	imageFound bool // ImageStatus returns a non-nil image when true

	// captured calls (slices to inspect which IDs were acted on)
	stoppedSandboxes  []string
	removedSandboxes  []string
	ranSandboxes      int
	createdContainers int
	startedContainers int
	pulledImages      []string
}

func (m *mockCRI) ListPodSandbox(_ context.Context, _ *runtimeapi.PodSandboxFilter) ([]*runtimeapi.PodSandbox, error) {
	return m.sandboxes, nil
}
func (m *mockCRI) ListContainers(_ context.Context, _ *runtimeapi.ContainerFilter) ([]*runtimeapi.Container, error) {
	return m.containers, nil
}
func (m *mockCRI) RunPodSandbox(_ context.Context, _ *runtimeapi.PodSandboxConfig) (string, error) {
	m.ranSandboxes++
	return "sandbox-new", nil
}
func (m *mockCRI) StopPodSandbox(_ context.Context, id string) error {
	m.stoppedSandboxes = append(m.stoppedSandboxes, id)
	return nil
}
func (m *mockCRI) RemovePodSandbox(_ context.Context, id string) error {
	m.removedSandboxes = append(m.removedSandboxes, id)
	return nil
}
func (m *mockCRI) PodSandboxStatus(_ context.Context, _ string) (*runtimeapi.PodSandboxStatus, error) {
	return &runtimeapi.PodSandboxStatus{
		Network: &runtimeapi.PodSandboxNetworkStatus{Ip: m.sandboxIP},
	}, nil
}
func (m *mockCRI) CreateContainer(_ context.Context, _ string, _ *runtimeapi.ContainerConfig, _ *runtimeapi.PodSandboxConfig) (string, error) {
	m.createdContainers++
	return "container-new", nil
}
func (m *mockCRI) StartContainer(_ context.Context, _ string) error {
	m.startedContainers++
	return nil
}
func (m *mockCRI) StopContainer(_ context.Context, _ string, _ int64) error { return nil }
func (m *mockCRI) RemoveContainer(_ context.Context, _ string) error        { return nil }
func (m *mockCRI) PullImage(_ context.Context, image string) error {
	m.pulledImages = append(m.pulledImages, image)
	return nil
}
func (m *mockCRI) ImageStatus(_ context.Context, _ string) (*runtimeapi.Image, error) {
	if m.imageFound {
		return &runtimeapi.Image{Id: "sha256:abc"}, nil
	}
	return nil, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestScheduler(mock *mockCRI) *Scheduler {
	return &Scheduler{
		store:  NewStore(),
		client: mock,
		parser: parser.NewParser(),
	}
}

const deploymentYAML = `apiVersion: apps/v1
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
`

func writeTempYAML(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sandboxFor(ns, name, id, configHash string) *runtimeapi.PodSandbox {
	return &runtimeapi.PodSandbox{
		Id:    id,
		State: runtimeapi.PodSandboxState_SANDBOX_READY,
		Labels: map[string]string{
			"kube-less/managed":   "true",
			"kube-less/namespace": ns,
			"kube-less/name":      name,
		},
		Annotations: map[string]string{
			"kube-less/config-hash": configHash,
		},
	}
}

// ── LoadManifests tests ───────────────────────────────────────────────────────

func TestLoadManifests_PopulatesStore(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	s := newTestScheduler(&mockCRI{})
	if err := s.LoadManifests([]string{dir}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ws := s.store.GetWorkload("default", "nginx")
	if ws == nil {
		t.Fatal("expected workload in store, got nil")
	}
	if ws.Manifest == nil {
		t.Error("expected Manifest to be set")
	}
	if ws.ConfigHash == "" {
		t.Error("expected ConfigHash to be set after LoadManifests")
	}
}

func TestLoadManifests_SetsFileWorkloads(t *testing.T) {
	dir := t.TempDir()
	path := writeTempYAML(t, dir, "app.yaml", deploymentYAML)

	s := newTestScheduler(&mockCRI{})
	_ = s.LoadManifests([]string{dir})

	keys := s.store.GetFileWorkloads(path)
	if len(keys) != 1 || keys[0] != "default/nginx" {
		t.Errorf("expected [default/nginx], got %v", keys)
	}
}

func TestLoadManifests_SkipsNonYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestScheduler(&mockCRI{})
	_ = s.LoadManifests([]string{dir})

	if len(s.store.GetWorkloads()) != 0 {
		t.Error("non-YAML files should not populate the store")
	}
}

func TestLoadManifests_MissingDir_ReturnsError(t *testing.T) {
	s := newTestScheduler(&mockCRI{})
	err := s.LoadManifests([]string{"/nonexistent/path"})
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

func TestLoadManifests_InvalidYAML_LogsAndContinues(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "bad.yaml", "not: valid: yaml: [}")
	writeTempYAML(t, dir, "good.yaml", deploymentYAML)

	s := newTestScheduler(&mockCRI{})
	// Should not error – bad file is skipped, good file is loaded
	if err := s.LoadManifests([]string{dir}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws := s.store.GetWorkload("default", "nginx"); ws == nil {
		t.Error("good.yaml should still be loaded despite bad.yaml")
	}
}

func TestLoadManifests_MultipleDeploymentsInOneFile(t *testing.T) {
	multi := deploymentYAML + "---\n" + `apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
      - name: redis
        image: redis:7
`
	dir := t.TempDir()
	writeTempYAML(t, dir, "multi.yaml", multi)

	s := newTestScheduler(&mockCRI{})
	_ = s.LoadManifests([]string{dir})

	if s.store.GetWorkload("default", "nginx") == nil {
		t.Error("expected nginx workload")
	}
	if s.store.GetWorkload("default", "redis") == nil {
		t.Error("expected redis workload")
	}
}

// ── SyncStateFromCRI tests ────────────────────────────────────────────────────

func TestSyncStateFromCRI_UpdatesExistingWorkload(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	mock := &mockCRI{sandboxIP: "10.88.0.5"}
	s := newTestScheduler(mock)
	_ = s.LoadManifests([]string{dir})

	hash := s.store.GetWorkload("default", "nginx").ConfigHash
	mock.sandboxes = []*runtimeapi.PodSandbox{sandboxFor("default", "nginx", "sandbox-1", hash)}
	mock.containers = []*runtimeapi.Container{{Id: "ctr-1"}}

	if err := s.SyncStateFromCRI(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ws := s.store.GetWorkload("default", "nginx")
	if ws.PodSandboxID != "sandbox-1" {
		t.Errorf("expected PodSandboxID=sandbox-1, got %s", ws.PodSandboxID)
	}
	if len(ws.ContainerIDs) != 1 || ws.ContainerIDs[0] != "ctr-1" {
		t.Errorf("expected ContainerIDs=[ctr-1], got %v", ws.ContainerIDs)
	}
	if ws.SandboxIP != "10.88.0.5" {
		t.Errorf("expected SandboxIP=10.88.0.5, got %s", ws.SandboxIP)
	}
	if ws.Status != PodStatusRunning {
		t.Errorf("expected status Running, got %s", ws.Status)
	}
}

func TestSyncStateFromCRI_PreservesDesiredConfigHash(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	mock := &mockCRI{}
	s := newTestScheduler(mock)
	_ = s.LoadManifests([]string{dir})

	desiredHash := s.store.GetWorkload("default", "nginx").ConfigHash

	// Sandbox has a DIFFERENT (old) hash – simulates a changed manifest
	mock.sandboxes = []*runtimeapi.PodSandbox{sandboxFor("default", "nginx", "sandbox-old", "old-hash")}

	_ = s.SyncStateFromCRI(context.Background())

	// ConfigHash must remain the DESIRED hash, not overwritten by the old sandbox hash
	if ws := s.store.GetWorkload("default", "nginx"); ws.ConfigHash != desiredHash {
		t.Errorf("ConfigHash should remain desired=%s, got %s", desiredHash, ws.ConfigHash)
	}
}

func TestSyncStateFromCRI_RemovesOrphanedSandbox(t *testing.T) {
	mock := &mockCRI{
		sandboxes: []*runtimeapi.PodSandbox{
			sandboxFor("default", "ghost", "sandbox-orphan", "hash-x"),
		},
	}
	s := newTestScheduler(mock)
	// Store is empty – no LoadManifests → ghost workload is orphaned

	if err := s.SyncStateFromCRI(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.stoppedSandboxes) != 1 || mock.stoppedSandboxes[0] != "sandbox-orphan" {
		t.Errorf("expected orphan to be stopped, stopped: %v", mock.stoppedSandboxes)
	}
	if len(mock.removedSandboxes) != 1 || mock.removedSandboxes[0] != "sandbox-orphan" {
		t.Errorf("expected orphan to be removed, removed: %v", mock.removedSandboxes)
	}
}

func TestSyncStateFromCRI_ManifestWithoutSandbox_IsNoOp(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	mock := &mockCRI{} // no running sandboxes
	s := newTestScheduler(mock)
	_ = s.LoadManifests([]string{dir})

	if err := s.SyncStateFromCRI(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nothing should be stopped/removed
	if len(mock.stoppedSandboxes)+len(mock.removedSandboxes) > 0 {
		t.Error("no CRI operations expected when sandbox is missing (reconcileAll will create it)")
	}
	// Workload should still be in store, just with no PodSandboxID
	ws := s.store.GetWorkload("default", "nginx")
	if ws == nil {
		t.Fatal("workload should still be in store")
	}
	if ws.PodSandboxID != "" {
		t.Errorf("expected empty PodSandboxID, got %s", ws.PodSandboxID)
	}
}

// ── reconcileAll tests ────────────────────────────────────────────────────────

func TestReconcileAll_CreatesWorkload_WhenNoSandbox(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	mock := &mockCRI{imageFound: true} // image already present, no pull
	s := newTestScheduler(mock)
	_ = s.LoadManifests([]string{dir})
	// No SyncStateFromCRI → sandbox not in store

	s.reconcileAll(context.Background())

	if mock.ranSandboxes != 1 {
		t.Errorf("expected 1 RunPodSandbox call, got %d", mock.ranSandboxes)
	}
	if mock.createdContainers != 1 {
		t.Errorf("expected 1 CreateContainer call, got %d", mock.createdContainers)
	}
}

func TestReconcileAll_NoOp_WhenHashMatches(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	mock := &mockCRI{}
	s := newTestScheduler(mock)
	_ = s.LoadManifests([]string{dir})

	hash := s.store.GetWorkload("default", "nginx").ConfigHash
	// Simulate sandbox already running with the matching hash
	mock.sandboxes = []*runtimeapi.PodSandbox{sandboxFor("default", "nginx", "sandbox-1", hash)}
	_ = s.SyncStateFromCRI(context.Background())

	s.reconcileAll(context.Background())

	if mock.ranSandboxes != 0 {
		t.Errorf("expected 0 RunPodSandbox calls (no-op), got %d", mock.ranSandboxes)
	}
}

func TestReconcileAll_Recreates_WhenHashDiffers(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	mock := &mockCRI{imageFound: true}
	s := newTestScheduler(mock)
	_ = s.LoadManifests([]string{dir})

	// Sandbox running with an OLD hash → change detected → recreate
	mock.sandboxes = []*runtimeapi.PodSandbox{sandboxFor("default", "nginx", "sandbox-old", "old-hash")}
	_ = s.SyncStateFromCRI(context.Background())

	s.reconcileAll(context.Background())

	if len(mock.stoppedSandboxes) != 1 {
		t.Errorf("expected old sandbox to be stopped, got %v", mock.stoppedSandboxes)
	}
	if mock.ranSandboxes != 1 {
		t.Errorf("expected 1 new sandbox to be created, got %d", mock.ranSandboxes)
	}
}

func TestReconcileAll_PullsImage_WhenNotPresent(t *testing.T) {
	dir := t.TempDir()
	writeTempYAML(t, dir, "nginx.yaml", deploymentYAML)

	mock := &mockCRI{imageFound: false} // image NOT present
	s := newTestScheduler(mock)
	_ = s.LoadManifests([]string{dir})

	s.reconcileAll(context.Background())

	if len(mock.pulledImages) != 1 {
		t.Errorf("expected 1 PullImage call, got %d", len(mock.pulledImages))
	}
}

func TestReconcileAll_SkipsManifestlessWorkload(t *testing.T) {
	mock := &mockCRI{}
	s := newTestScheduler(mock)

	// Insert a workload directly without manifest (edge case)
	s.store.UpdateWorkload("default", "ghost", makeDeployment("default", "ghost"))
	s.store.workloads["default/ghost"].Manifest = nil // clear manifest

	s.reconcileAll(context.Background())

	if mock.ranSandboxes != 0 {
		t.Error("workload without manifest should not trigger RunPodSandbox")
	}
}
