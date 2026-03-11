package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
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
