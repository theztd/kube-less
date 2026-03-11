package scheduler

import "testing"

func workloadState(sandboxID, configHash string) *WorkloadState {
	ws := &WorkloadState{
		Name:         "web",
		Namespace:    "default",
		PodSandboxID: sandboxID,
		ConfigHash:   configHash,
		Status:       PodStatusRunning,
	}
	return ws
}

func TestCompare_NoDesired(t *testing.T) {
	if a := compare(nil, "sandbox-1", "hash-1"); a != ActionDelete {
		t.Errorf("expected ActionDelete, got %s", a)
	}
}

func TestCompare_NoSandbox(t *testing.T) {
	ws := workloadState("", "hash-1")
	if a := compare(ws, "", ""); a != ActionCreate {
		t.Errorf("expected ActionCreate, got %s", a)
	}
}

func TestCompare_SameHash(t *testing.T) {
	ws := workloadState("sandbox-1", "hash-abc")
	if a := compare(ws, "sandbox-1", "hash-abc"); a != ActionNone {
		t.Errorf("expected ActionNone, got %s", a)
	}
}

func TestCompare_DifferentHash(t *testing.T) {
	ws := workloadState("sandbox-1", "hash-new")
	if a := compare(ws, "sandbox-1", "hash-old"); a != ActionRecreate {
		t.Errorf("expected ActionRecreate, got %s", a)
	}
}

func TestAction_String(t *testing.T) {
	cases := map[Action]string{
		ActionNone:     "None",
		ActionCreate:   "Create",
		ActionRecreate: "Recreate",
		ActionDelete:   "Delete",
	}
	for action, want := range cases {
		if got := action.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", action, got, want)
		}
	}
}
