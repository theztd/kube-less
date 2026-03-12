package probe

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ReadyUpdater is implemented by the store and called when readiness changes.
type ReadyUpdater interface {
	SetWorkloadReady(namespace, name string, ready bool)
}

// Runner manages per-workload HTTP readiness probe goroutines.
type Runner struct {
	store   ReadyUpdater
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	client  *http.Client
}

// NewRunner creates a new probe Runner.
func NewRunner(store ReadyUpdater) *Runner {
	return &Runner{
		store:   store,
		cancels: make(map[string]context.CancelFunc),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Watch starts probe goroutines for containers that declare a readinessProbe.HTTPGet.
// Workloads with no readiness probe are marked ready immediately (optimistic).
func (r *Runner) Watch(dep *appsv1.Deployment, sandboxIP string) {
	ns, name := dep.Namespace, dep.Name
	key := keyFor(ns, name)

	r.mu.Lock()
	if cancel, ok := r.cancels[key]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancels[key] = cancel
	r.mu.Unlock()

	hasProbe := false
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.ReadinessProbe != nil && c.ReadinessProbe.HTTPGet != nil {
			hasProbe = true
			go r.runProbe(ctx, ns, name, sandboxIP, c)
		}
	}
	if !hasProbe {
		r.store.SetWorkloadReady(ns, name, true)
	}
}

// Stop cancels the probe goroutine for a workload and marks it not-ready.
func (r *Runner) Stop(namespace, name string) {
	key := keyFor(namespace, name)

	r.mu.Lock()
	if cancel, ok := r.cancels[key]; ok {
		cancel()
		delete(r.cancels, key)
	}
	r.mu.Unlock()

	r.store.SetWorkloadReady(namespace, name, false)
}

func (r *Runner) runProbe(ctx context.Context, ns, name, ip string, c v1.Container) {
	p := c.ReadinessProbe
	hg := p.HTTPGet

	initialDelay := time.Duration(p.InitialDelaySeconds) * time.Second
	period := time.Duration(p.PeriodSeconds) * time.Second
	if period == 0 {
		period = 10 * time.Second
	}
	successThreshold := p.SuccessThreshold
	if successThreshold == 0 {
		successThreshold = 1
	}
	failureThreshold := p.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = 3
	}

	port := resolvePort(hg.Port, c)
	path := hg.Path
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("http://%s:%d%s", ip, port, path)
	log.Printf("probe: %s/%s initialDelay=%s period=%s url=%s", ns, name, initialDelay, period, url)

	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	var successes, failures int32
	ready := false

	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.doGet(url) {
				failures = 0
				successes++
				if !ready && successes >= successThreshold {
					ready = true
					successes = 0
					r.store.SetWorkloadReady(ns, name, true)
					log.Printf("probe: %s/%s READY", ns, name)
				}
			} else {
				successes = 0
				failures++
				if ready && failures >= failureThreshold {
					ready = false
					failures = 0
					r.store.SetWorkloadReady(ns, name, false)
					log.Printf("probe: %s/%s NOT READY", ns, name)
				}
			}
		}
	}
}

func (r *Runner) doGet(url string) bool {
	resp, err := r.client.Get(url) //nolint:noctx
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func resolvePort(p intstr.IntOrString, c v1.Container) int32 {
	if p.Type == intstr.Int {
		return p.IntVal
	}
	for _, cp := range c.Ports {
		if cp.Name == p.String() {
			return cp.ContainerPort
		}
	}
	return 80
}

func keyFor(namespace, name string) string {
	return namespace + "/" + name
}
