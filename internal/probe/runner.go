// Package probe runs per-workload HTTP GET readiness probes.
//
// Runner manages a goroutine per workload that periodically sends HTTP GET
// requests to the probe endpoint (resolved from the sandbox IP and the
// container port). It honours initialDelaySeconds, periodSeconds,
// successThreshold and failureThreshold from the Kubernetes readinessProbe
// spec. When a threshold is crossed, it calls ReadyUpdater.SetWorkloadReady to
// update the scheduler Store. Workloads without a readinessProbe are marked
// ready immediately after sandbox creation.
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

// ReadyUpdater is implemented by *scheduler.Store. Defined here to avoid an
// import cycle between the probe and scheduler packages.
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

// NewRunner creates a new Runner backed by the given ReadyUpdater.
func NewRunner(store ReadyUpdater) *Runner {
	return &Runner{
		store:   store,
		cancels: make(map[string]context.CancelFunc),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Watch starts a readiness probe goroutine for the given deployment and
// sandbox IP. If the deployment has no readinessProbe, the workload is
// marked ready immediately without starting a goroutine.
// Any previously running probe for the same workload is cancelled first.
func (r *Runner) Watch(dep *appsv1.Deployment, sandboxIP string) {
	key := dep.Namespace + "/" + dep.Name

	r.mu.Lock()
	if cancel, ok := r.cancels[key]; ok {
		cancel()
		delete(r.cancels, key)
	}
	r.mu.Unlock()

	// Check if any container has a readiness probe.
	hasProbe := false
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.ReadinessProbe != nil && c.ReadinessProbe.HTTPGet != nil {
			hasProbe = true
			break
		}
	}

	if !hasProbe {
		r.store.SetWorkloadReady(dep.Namespace, dep.Name, true)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.cancels[key] = cancel
	r.mu.Unlock()

	// Start one goroutine per container with an httpGet probe.
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil {
			continue
		}
		go r.runProbe(ctx, dep.Namespace, dep.Name, sandboxIP, c)
	}
}

// Stop cancels the probe goroutine for the given workload and marks it
// not-ready.
func (r *Runner) Stop(namespace, name string) {
	key := namespace + "/" + name

	r.mu.Lock()
	if cancel, ok := r.cancels[key]; ok {
		cancel()
		delete(r.cancels, key)
	}
	r.mu.Unlock()

	r.store.SetWorkloadReady(namespace, name, false)
}

// runProbe is the per-container probe goroutine. It respects initialDelaySeconds,
// periodSeconds, successThreshold and failureThreshold from the probe spec.
func (r *Runner) runProbe(ctx context.Context, namespace, name, sandboxIP string, c v1.Container) {
	p := c.ReadinessProbe
	hg := p.HTTPGet

	initialDelay := time.Duration(p.InitialDelaySeconds) * time.Second
	period := time.Duration(p.PeriodSeconds) * time.Second
	if period <= 0 {
		period = 10 * time.Second
	}
	successThreshold := p.SuccessThreshold
	if successThreshold <= 0 {
		successThreshold = 1
	}
	failureThreshold := p.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = 3
	}

	if initialDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(initialDelay):
		}
	}

	port := resolvePort(hg.Port, c)
	path := hg.Path
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("http://%s:%d%s", sandboxIP, port, path)

	var successes, failures int32

	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resp, err := r.client.Get(url) //nolint:noctx
			if err == nil {
				resp.Body.Close()
			}

			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400 {
				failures = 0
				successes++
				if successes >= successThreshold {
					log.Printf("probe: %s/%s ready (url=%s)", namespace, name, url)
					r.store.SetWorkloadReady(namespace, name, true)
					successes = 0
				}
			} else {
				successes = 0
				failures++
				if failures >= failureThreshold {
					log.Printf("probe: %s/%s not-ready (url=%s)", namespace, name, url)
					r.store.SetWorkloadReady(namespace, name, false)
					failures = 0
				}
			}
		}
	}
}

// resolvePort converts an IntOrString port to an int32. For string values it
// looks up a named port in the container's Ports list. Falls back to 80.
func resolvePort(p intstr.IntOrString, c v1.Container) int32 {
	if p.Type == intstr.Int {
		return p.IntVal
	}
	for _, cp := range c.Ports {
		if cp.Name == p.StrVal {
			return cp.ContainerPort
		}
	}
	return 80
}
