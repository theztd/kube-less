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
