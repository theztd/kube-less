// Package api exposes a lightweight HTTP debug API for kube-less.
//
// Server provides two endpoints:
//
//   - GET /status    – returns JSON array of all WorkloadState objects (name,
//     namespace, status, ready, sandbox IP, container IDs, …).
//   - GET /endpoints – returns JSON array of Endpoint objects for workloads
//     that are ready and have a sandbox IP assigned; useful for service
//     discovery and health checks.
package api
