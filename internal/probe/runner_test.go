package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// mockStore records SetWorkloadReady calls.
type mockStore struct {
	mu    sync.Mutex
	calls []readyCall
}

type readyCall struct {
	namespace, name string
	ready           bool
}

func (m *mockStore) SetWorkloadReady(namespace, name string, ready bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, readyCall{namespace, name, ready})
}

func (m *mockStore) lastCall() (readyCall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return readyCall{}, false
	}
	return m.calls[len(m.calls)-1], true
}

func (m *mockStore) waitForCall(t *testing.T, timeout time.Duration) readyCall {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c, ok := m.lastCall(); ok {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for SetWorkloadReady call")
	return readyCall{}
}

func baseDeployment(ns, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{Name: "web", Image: "nginx:latest"},
					},
				},
			},
		},
	}
}

// newTestRunner creates a Runner whose HTTP client is replaced with the given one.
func newTestRunner(store *mockStore, client *http.Client) *Runner {
	return &Runner{
		store:   store,
		cancels: make(map[string]context.CancelFunc),
		client:  client,
	}
}

// ── No probe → optimistic ready ───────────────────────────────────────────────

func TestWatch_NoProbe_MarksReadyImmediately(t *testing.T) {
	store := &mockStore{}
	r := NewRunner(store)

	dep := baseDeployment("default", "nginx")
	r.Watch(dep, "10.88.0.1")

	c := store.waitForCall(t, time.Second)
	if !c.ready {
		t.Errorf("expected ready=true for workload without probe, got false")
	}
	if c.namespace != "default" || c.name != "nginx" {
		t.Errorf("unexpected workload: %s/%s", c.namespace, c.name)
	}
}

// ── HTTP probe success → ready ────────────────────────────────────────────────

func TestWatch_HTTPProbe_MarksReadyOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().(*net.TCPAddr)
	store := &mockStore{}
	r := newTestRunner(store, srv.Client())

	dep := baseDeployment("default", "api")
	dep.Spec.Template.Spec.Containers[0].ReadinessProbe = &v1.Probe{
		ProbeHandler: v1.ProbeHandler{
			HTTPGet: &v1.HTTPGetAction{
				Path: "/health",
				Port: intstr.FromInt(addr.Port),
			},
		},
		PeriodSeconds:    1,
		SuccessThreshold: 1,
		FailureThreshold: 3,
	}

	r.Watch(dep, "127.0.0.1")
	defer r.Stop("default", "api")

	c := store.waitForCall(t, 5*time.Second)
	if !c.ready {
		t.Errorf("expected ready=true after successful HTTP probe")
	}
}

// ── HTTP probe failure → stays not-ready ──────────────────────────────────────

func TestWatch_HTTPProbe_StaysNotReadyOn500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().(*net.TCPAddr)
	store := &mockStore{}
	r := newTestRunner(store, srv.Client())

	dep := baseDeployment("default", "broken")
	dep.Spec.Template.Spec.Containers[0].ReadinessProbe = &v1.Probe{
		ProbeHandler: v1.ProbeHandler{
			HTTPGet: &v1.HTTPGetAction{
				Path: "/health",
				Port: intstr.FromInt(addr.Port),
			},
		},
		PeriodSeconds:    1,
		SuccessThreshold: 1,
		FailureThreshold: 1,
	}

	r.Watch(dep, "127.0.0.1")
	defer r.Stop("default", "broken")

	// Run for 2 probe ticks; should never call setReady(true).
	time.Sleep(2500 * time.Millisecond)
	if c, ok := store.lastCall(); ok && c.ready {
		t.Error("probe returning 500 should not mark workload ready")
	}
}

// ── Stop → not-ready ──────────────────────────────────────────────────────────

func TestStop_MarksNotReady(t *testing.T) {
	store := &mockStore{}
	r := NewRunner(store)

	dep := baseDeployment("default", "nginx")
	r.Watch(dep, "10.88.0.1") // no probe → immediately ready
	store.waitForCall(t, time.Second)

	r.Stop("default", "nginx")

	c := store.waitForCall(t, time.Second)
	if c.ready {
		t.Error("Stop should mark workload not-ready")
	}
}

// ── resolvePort ────────────────────────────────────────────────────────────────

func TestResolvePort_Int(t *testing.T) {
	p := intstr.FromInt(8080)
	c := v1.Container{}
	if got := resolvePort(p, c); got != 8080 {
		t.Errorf("expected 8080, got %d", got)
	}
}

func TestResolvePort_NamedPort(t *testing.T) {
	p := intstr.FromString("http")
	c := v1.Container{
		Ports: []v1.ContainerPort{
			{Name: "http", ContainerPort: 8080},
		},
	}
	if got := resolvePort(p, c); got != 8080 {
		t.Errorf("expected 8080, got %d", got)
	}
}

func TestResolvePort_UnknownName_FallsBackTo80(t *testing.T) {
	p := intstr.FromString("unknown")
	c := v1.Container{}
	if got := resolvePort(p, c); got != 80 {
		t.Errorf("expected 80 fallback, got %d", got)
	}
}
