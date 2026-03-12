// Package cri provides a client for the Container Runtime Interface (CRI) and
// helpers to translate Kubernetes Deployment manifests into CRI API calls.
//
// Client wraps the gRPC RuntimeServiceClient and ImageServiceClient and exposes
// high-level methods: CreateSandbox, CreateContainer, StartContainer,
// StopAndRemoveSandbox, and ListPodSandboxes. Builder translates a
// v1.PodSpec into CRI PodSandboxConfig / ContainerConfig structs, handling
// image pulls, environment variables, volume mounts (ConfigMap / Secret), and
// resource limits.
package cri
