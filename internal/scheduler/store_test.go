package scheduler

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func makeDeployment(namespace, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func TestUpdateWorkload_CreatesNewEntry(t *testing.T) {
	s := NewStore()
	s.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))

	workloads := s.GetWorkloads()
	if len(workloads) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(workloads))
	}
	if workloads[0].Name != "nginx" {
		t.Errorf("expected name %q, got %q", "nginx", workloads[0].Name)
	}
	if workloads[0].Namespace != "default" {
		t.Errorf("expected namespace %q, got %q", "default", workloads[0].Namespace)
	}
	if workloads[0].Status != PodStatusPending {
		t.Errorf("expected initial status %q, got %q", PodStatusPending, workloads[0].Status)
	}
}

func TestUpdateWorkload_UpdatesExistingEntry(t *testing.T) {
	s := NewStore()
	dep1 := makeDeployment("default", "nginx")
	s.UpdateWorkload("default", "nginx", dep1)

	dep2 := makeDeployment("default", "nginx")
	dep2.Labels = map[string]string{"version": "2"}
	s.UpdateWorkload("default", "nginx", dep2)

	workloads := s.GetWorkloads()
	if len(workloads) != 1 {
		t.Fatalf("expected 1 workload after update, got %d", len(workloads))
	}
	if workloads[0].Manifest.Labels["version"] != "2" {
		t.Error("expected manifest to be updated to dep2")
	}
}

func TestUpdateWorkload_MultipleNamespaces(t *testing.T) {
	s := NewStore()
	s.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))
	s.UpdateWorkload("production", "nginx", makeDeployment("production", "nginx"))

	workloads := s.GetWorkloads()
	if len(workloads) != 2 {
		t.Fatalf("expected 2 workloads, got %d", len(workloads))
	}
}

func TestGetWorkloads_EmptyStore(t *testing.T) {
	s := NewStore()
	workloads := s.GetWorkloads()
	if workloads == nil {
		t.Error("expected non-nil slice for empty store")
	}
	if len(workloads) != 0 {
		t.Errorf("expected 0 workloads, got %d", len(workloads))
	}
}

func TestGetWorkloads_ReturnsCopy(t *testing.T) {
	s := NewStore()
	s.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))

	workloads := s.GetWorkloads()
	workloads[0].Name = "mutated"

	// Original should be unchanged
	fresh := s.GetWorkloads()
	if fresh[0].Name != "nginx" {
		t.Error("GetWorkloads should return copies, original should not be mutated")
	}
}

func TestDeleteWorkload_RemovesEntry(t *testing.T) {
	s := NewStore()
	s.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))
	s.DeleteWorkload("default", "nginx")

	if len(s.GetWorkloads()) != 0 {
		t.Error("expected empty store after delete")
	}
}

func TestDeleteWorkload_NonExistent_NoError(t *testing.T) {
	s := NewStore()
	// Should not panic
	s.DeleteWorkload("default", "nonexistent")
}

func TestUpdatePodStatus_UpdatesSandboxID(t *testing.T) {
	s := NewStore()
	s.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))
	s.UpdatePodStatus("default", "nginx", "sandbox-abc123", PodStatusRunning)

	workloads := s.GetWorkloads()
	if workloads[0].PodSandboxID != "sandbox-abc123" {
		t.Errorf("expected sandbox ID %q, got %q", "sandbox-abc123", workloads[0].PodSandboxID)
	}
	if workloads[0].Status != PodStatusRunning {
		t.Errorf("expected status %q, got %q", PodStatusRunning, workloads[0].Status)
	}
}

func TestUpdatePodStatus_NonExistent_IsNoOp(t *testing.T) {
	s := NewStore()
	// Should not panic, workload doesn't exist
	s.UpdatePodStatus("default", "ghost", "id-xyz", PodStatusRunning)
	if len(s.GetWorkloads()) != 0 {
		t.Error("expected store to remain empty")
	}
}

func TestTranslateCRIState_Ready(t *testing.T) {
	status := TranslateCRIState(runtimeapi.PodSandboxState_SANDBOX_READY)
	if status != PodStatusRunning {
		t.Errorf("expected PodStatusRunning, got %s", status)
	}
}

func TestTranslateCRIState_NotReady(t *testing.T) {
	status := TranslateCRIState(runtimeapi.PodSandboxState_SANDBOX_NOTREADY)
	if status != PodStatusPending {
		t.Errorf("expected PodStatusPending, got %s", status)
	}
}

func TestKeyFunc_CorrectFormat(t *testing.T) {
	key := keyFunc("production", "api-server")
	expected := "production/api-server"
	if key != expected {
		t.Errorf("expected key %q, got %q", expected, key)
	}
}

func TestGetWorkload_ReturnsNilForMissing(t *testing.T) {
	s := NewStore()
	if ws := s.GetWorkload("default", "missing"); ws != nil {
		t.Errorf("expected nil, got %+v", ws)
	}
}

func TestGetWorkload_ReturnsCopy(t *testing.T) {
	s := NewStore()
	s.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))

	ws := s.GetWorkload("default", "nginx")
	ws.Name = "mutated"

	fresh := s.GetWorkload("default", "nginx")
	if fresh.Name != "nginx" {
		t.Error("GetWorkload should return a copy, original should not be mutated")
	}
}

func TestSetWorkloadRuntime(t *testing.T) {
	s := NewStore()
	s.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))
	s.SetWorkloadRuntime("default", "nginx", "sandbox-1", []string{"ctr-a", "ctr-b"}, "10.88.0.5", "hash-abc")

	ws := s.GetWorkload("default", "nginx")
	if ws.PodSandboxID != "sandbox-1" {
		t.Errorf("expected sandbox-1, got %s", ws.PodSandboxID)
	}
	if len(ws.ContainerIDs) != 2 {
		t.Errorf("expected 2 container IDs, got %d", len(ws.ContainerIDs))
	}
	if ws.SandboxIP != "10.88.0.5" {
		t.Errorf("expected 10.88.0.5, got %s", ws.SandboxIP)
	}
	if ws.ConfigHash != "hash-abc" {
		t.Errorf("expected hash-abc, got %s", ws.ConfigHash)
	}
	if ws.Status != PodStatusRunning {
		t.Errorf("expected Running, got %s", ws.Status)
	}
	if ws.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

func TestFileWorkloads_SetGetDelete(t *testing.T) {
	s := NewStore()
	s.SetFileWorkloads("/manifests/app.yaml", []string{"default/nginx", "default/redis"})

	keys := s.GetFileWorkloads("/manifests/app.yaml")
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != "default/nginx" || keys[1] != "default/redis" {
		t.Errorf("unexpected keys: %v", keys)
	}

	s.DeleteFileWorkloads("/manifests/app.yaml")
	if k := s.GetFileWorkloads("/manifests/app.yaml"); len(k) != 0 {
		t.Errorf("expected empty after delete, got %v", k)
	}
}

func TestFileWorkloads_GetReturnsCopy(t *testing.T) {
	s := NewStore()
	s.SetFileWorkloads("/manifests/app.yaml", []string{"default/nginx"})

	keys := s.GetFileWorkloads("/manifests/app.yaml")
	keys[0] = "mutated"

	fresh := s.GetFileWorkloads("/manifests/app.yaml")
	if fresh[0] != "default/nginx" {
		t.Error("GetFileWorkloads should return a copy")
	}
}

func TestComputeConfigHash_Deterministic(t *testing.T) {
	dep := makeDeployment("default", "nginx")
	h1 := ComputeConfigHash(dep)
	h2 := ComputeConfigHash(dep)
	if h1 != h2 {
		t.Error("ComputeConfigHash should be deterministic")
	}
}

func TestComputeConfigHash_DifferentSpec(t *testing.T) {
	dep1 := makeDeployment("default", "nginx")
	dep2 := makeDeployment("default", "nginx")
	dep2.Spec.Template.Spec.Containers = []v1.Container{
		{Name: "nginx", Image: "nginx:1.25"},
	}

	if ComputeConfigHash(dep1) == ComputeConfigHash(dep2) {
		t.Error("different specs should produce different hashes")
	}
}

// ── ConfigMap store tests ─────────────────────────────────────────────────────

func TestUpdateConfigMap_GetConfigMap(t *testing.T) {
	s := NewStore()
	cm := &v1.ConfigMap{Data: map[string]string{"key": "value"}}
	s.UpdateConfigMap("default", "app-config", cm)

	got := s.GetConfigMap("default", "app-config")
	if got == nil {
		t.Fatal("expected ConfigMap, got nil")
	}
	if got.Data["key"] != "value" {
		t.Errorf("expected value=value, got %s", got.Data["key"])
	}
}

func TestGetConfigMap_Missing_ReturnsNil(t *testing.T) {
	s := NewStore()
	if cm := s.GetConfigMap("default", "missing"); cm != nil {
		t.Errorf("expected nil, got %+v", cm)
	}
}

func TestDeleteConfigMap(t *testing.T) {
	s := NewStore()
	s.UpdateConfigMap("default", "app-config", &v1.ConfigMap{})
	s.DeleteConfigMap("default", "app-config")
	if cm := s.GetConfigMap("default", "app-config"); cm != nil {
		t.Error("expected nil after delete")
	}
}

func TestGetAllConfigMaps_ReturnsCopy(t *testing.T) {
	s := NewStore()
	s.UpdateConfigMap("default", "cm1", &v1.ConfigMap{Data: map[string]string{"k": "v"}})
	s.UpdateConfigMap("default", "cm2", &v1.ConfigMap{Data: map[string]string{"k": "v2"}})

	all := s.GetAllConfigMaps()
	if len(all) != 2 {
		t.Fatalf("expected 2 configmaps, got %d", len(all))
	}
	// Mutating the copy should not affect the store
	delete(all, "default/cm1")
	if s.GetConfigMap("default", "cm1") == nil {
		t.Error("store should not be mutated by modifying GetAllConfigMaps result")
	}
}

func TestUpdateConfigMap_RecomputesWorkloadHash(t *testing.T) {
	s := NewStore()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{{
						Name: "web",
						Env: []v1.EnvVar{{
							Name: "DB_HOST",
							ValueFrom: &v1.EnvVarSource{
								ConfigMapKeyRef: &v1.ConfigMapKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: "app-config"},
									Key:                  "db_host",
								},
							},
						}},
					}},
				},
			},
		},
	}
	s.UpdateWorkload("default", "web", dep)
	hashBefore := s.GetWorkload("default", "web").ConfigHash

	// Update the referenced ConfigMap
	s.UpdateConfigMap("default", "app-config", &v1.ConfigMap{
		Data: map[string]string{"db_host": "postgres:5432"},
	})
	hashAfter := s.GetWorkload("default", "web").ConfigHash

	if hashBefore == hashAfter {
		t.Error("workload hash should change when referenced ConfigMap is updated")
	}
}

// ── Secret store tests ────────────────────────────────────────────────────────

func TestUpdateSecret_GetSecret(t *testing.T) {
	s := NewStore()
	sec := &v1.Secret{Data: map[string][]byte{"api_key": []byte("secret")}}
	s.UpdateSecret("default", "app-secret", sec)

	got := s.GetSecret("default", "app-secret")
	if got == nil {
		t.Fatal("expected Secret, got nil")
	}
	if string(got.Data["api_key"]) != "secret" {
		t.Errorf("expected api_key=secret, got %s", string(got.Data["api_key"]))
	}
}

func TestDeleteSecret(t *testing.T) {
	s := NewStore()
	s.UpdateSecret("default", "app-secret", &v1.Secret{})
	s.DeleteSecret("default", "app-secret")
	if sec := s.GetSecret("default", "app-secret"); sec != nil {
		t.Error("expected nil after delete")
	}
}

func TestUpdateSecret_RecomputesWorkloadHash(t *testing.T) {
	s := NewStore()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{{
						Name: "web",
						Env: []v1.EnvVar{{
							Name: "API_KEY",
							ValueFrom: &v1.EnvVarSource{
								SecretKeyRef: &v1.SecretKeySelector{
									LocalObjectReference: v1.LocalObjectReference{Name: "app-secret"},
									Key:                  "api_key",
								},
							},
						}},
					}},
				},
			},
		},
	}
	s.UpdateWorkload("default", "web", dep)
	hashBefore := s.GetWorkload("default", "web").ConfigHash

	s.UpdateSecret("default", "app-secret", &v1.Secret{
		Data: map[string][]byte{"api_key": []byte("newsecret")},
	})
	hashAfter := s.GetWorkload("default", "web").ConfigHash

	if hashBefore == hashAfter {
		t.Error("workload hash should change when referenced Secret is updated")
	}
}

// ── File → CM/Secret mapping tests ───────────────────────────────────────────

func TestFileCMs_SetGetDelete(t *testing.T) {
	s := NewStore()
	s.SetFileCMs("/manifests/config.yaml", []string{"default/cm1", "default/cm2"})

	keys := s.GetFileCMs("/manifests/config.yaml")
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}

	s.DeleteFileCMs("/manifests/config.yaml")
	if k := s.GetFileCMs("/manifests/config.yaml"); len(k) != 0 {
		t.Errorf("expected empty after delete, got %v", k)
	}
}

func TestFileSecrets_SetGetDelete(t *testing.T) {
	s := NewStore()
	s.SetFileSecrets("/manifests/secret.yaml", []string{"default/mysecret"})

	keys := s.GetFileSecrets("/manifests/secret.yaml")
	if len(keys) != 1 || keys[0] != "default/mysecret" {
		t.Errorf("unexpected keys: %v", keys)
	}

	s.DeleteFileSecrets("/manifests/secret.yaml")
	if k := s.GetFileSecrets("/manifests/secret.yaml"); len(k) != 0 {
		t.Errorf("expected empty after delete, got %v", k)
	}
}
