package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kube-less/internal/scheduler"
)

func newTestServer() (*Server, *scheduler.Store) {
	store := scheduler.NewStore()
	return NewServer(store, 8080), store
}

func makeDeployment(ns, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
}

// ── GET /status ───────────────────────────────────────────────────────────────

func TestHandleStatus_EmptyStore(t *testing.T) {
	srv, _ := newTestServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)

	srv.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var result []interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array, got %v", result)
	}
}

func TestHandleStatus_ReturnsWorkloads(t *testing.T) {
	srv, store := newTestServer()
	store.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))
	store.UpdateWorkload("prod", "api", makeDeployment("prod", "api"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	srv.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var result []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 workloads, got %d", len(result))
	}
}

func TestHandleStatus_WorkloadFields(t *testing.T) {
	srv, store := newTestServer()
	store.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	srv.handleStatus(rec, req)

	var result []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	w := result[0]
	if w["name"] != "nginx" {
		t.Errorf("expected name=nginx, got %v", w["name"])
	}
	if w["namespace"] != "default" {
		t.Errorf("expected namespace=default, got %v", w["namespace"])
	}
	if w["status"] != "Pending" {
		t.Errorf("expected status=Pending, got %v", w["status"])
	}
}

func TestHandleStatus_MethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/status", nil)
		srv.handleStatus(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rec.Code)
		}
	}
}

func TestNewServer_SetsPort(t *testing.T) {
	store := scheduler.NewStore()
	srv := NewServer(store, 9999)
	if srv.port != 9999 {
		t.Errorf("expected port 9999, got %d", srv.port)
	}
}

// ── GET /endpoints ────────────────────────────────────────────────────────────

func makeDeploymentWithPort(ns, name string, port int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "web",
							Image: "nginx:latest",
							Ports: []v1.ContainerPort{
								{ContainerPort: port},
							},
						},
					},
				},
			},
		},
	}
}

func TestHandleEndpoints_EmptyWhenNoReadyWorkloads(t *testing.T) {
	srv, store := newTestServer()
	store.UpdateWorkload("default", "nginx", makeDeployment("default", "nginx"))
	// Not marking ready, not setting IP

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/endpoints", nil)
	srv.handleEndpoints(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var result []interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array for non-ready workloads, got %v", result)
	}
}

func TestHandleEndpoints_ReturnsReadyWorkload(t *testing.T) {
	srv, store := newTestServer()
	dep := makeDeploymentWithPort("default", "nginx", 80)
	store.UpdateWorkload("default", "nginx", dep)
	store.SetWorkloadRuntime("default", "nginx", "sb-1", []string{"ctr-1"}, "10.88.0.5", "hash")
	store.SetWorkloadReady("default", "nginx", true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/endpoints", nil)
	srv.handleEndpoints(rec, req)

	var result []Endpoint
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(result))
	}
	ep := result[0]
	if ep.Namespace != "default" || ep.Name != "nginx" {
		t.Errorf("unexpected endpoint identity: %s/%s", ep.Namespace, ep.Name)
	}
	if ep.IP != "10.88.0.5" {
		t.Errorf("expected IP 10.88.0.5, got %s", ep.IP)
	}
	if len(ep.Ports) != 1 || ep.Ports[0] != 80 {
		t.Errorf("expected ports=[80], got %v", ep.Ports)
	}
}

func TestHandleEndpoints_FiltersNonReadyAndNoIP(t *testing.T) {
	srv, store := newTestServer()

	// ready but no IP
	dep1 := makeDeploymentWithPort("default", "noip", 80)
	store.UpdateWorkload("default", "noip", dep1)
	store.SetWorkloadReady("default", "noip", true)

	// has IP but not ready
	dep2 := makeDeploymentWithPort("default", "notready", 80)
	store.UpdateWorkload("default", "notready", dep2)
	store.SetWorkloadRuntime("default", "notready", "sb-2", nil, "10.88.0.6", "hash")

	// ready with IP
	dep3 := makeDeploymentWithPort("default", "ok", 8080)
	store.UpdateWorkload("default", "ok", dep3)
	store.SetWorkloadRuntime("default", "ok", "sb-3", nil, "10.88.0.7", "hash")
	store.SetWorkloadReady("default", "ok", true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/endpoints", nil)
	srv.handleEndpoints(rec, req)

	var result []Endpoint
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 endpoint (only 'ok'), got %d: %+v", len(result), result)
	}
	if result[0].Name != "ok" {
		t.Errorf("expected endpoint for 'ok', got %s", result[0].Name)
	}
}

func TestHandleEndpoints_MethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/endpoints", nil)
	srv.handleEndpoints(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
