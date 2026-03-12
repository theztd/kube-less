// Package scheduler is the core reconciliation engine for kube-less.
//
// It maintains an in-memory Store of desired workload state (parsed from YAML
// manifests) and actual runtime state (pod sandbox IDs, container IDs, IPs).
// On every sync interval the Engine calls ReconcileAll, which compares desired
// vs actual state and issues Create / Recreate / Delete actions via the CRI
// client. ConfigMap and Secret changes automatically invalidate the affected
// workload hashes, triggering a rolling restart.
package scheduler
