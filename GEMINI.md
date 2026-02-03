# Project Context: EdgePod Runner (EPR)

## Overview
**EdgePod Runner (EPR)** (also referred to as `kube-less`) is a minimalist, autonomous agent designed to be written in **Go**. Its core mission is to manage the lifecycle of containers on a single edge node using standard Kubernetes manifests (Deployment, ConfigMap, Secret) entirely **offline**, without any dependency on the Kubernetes Control Plane (etcd, API server, Scheduler).

*   **Vision:** Single binary, CRI-compatible, Offline-First, No Magic.
*   **Source of Truth:** Local directory with YAML files.

## Technical Architecture
The architecture consists of three logical layers:

1.  **Input Watcher:**
    *   Watches `/etc/epr/manifests/` for filesystem changes.
    *   Detects additions/removals/updates of `apps/v1.Deployment`, `v1.ConfigMap`, and `v1.Secret`.

2.  **Hydration Engine:**
    *   Acts as a local logic layer to replace the K8s API server.
    *   Translates `Deployment` templates into `Pod` specs.
    *   Handles ConfigMap/Secret injection by mapping them to host files or environment variables.

3.  **Container Lifecycle Manager (CRI):**
    *   Direct gRPC client for `containerd` or `CRI-O`.
    *   Maintains a sync loop to reconcile running containers with the local manifests.

## Current Status
**Phase:** Initialization / Design
*   The repository currently contains project documentation and configuration files.
*   **No source code** has been implemented yet.
*   **Next Step:** Initialize the Go module and begin Phase 1 of the roadmap.

## Development Roadmap (MVP)
*   **Phase 1:** Establish connection to the CRI socket and implement listing of running pods.
*   **Phase 2:** Parse a local YAML Deployment file and successfully launch a Pod (e.g., Nginx).
*   **Phase 3:** Implement ConfigMap injection mechanisms.

## Technology Stack (Planned)
*   **Language:** Go (Golang)
*   **Interface:** CRI (Container Runtime Interface) via gRPC
*   **Runtime Support:** `containerd`, `CRI-O`
