package scheduler

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
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
