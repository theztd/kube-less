# Project Context: EdgePod Runner (EPR)

## Overview
**EdgePod Runner (EPR)** (also referred to as `kube-less`) is a minimalist, autonomous agent designed to be written in **Go**. Its core mission is to manage the lifecycle of containers on a single edge node using standard Kubernetes manifests (Deployment, ConfigMap, Secret) entirely **offline**, without any dependency on the Kubernetes Control Plane (etcd, API server, Scheduler).

*   **Vision:** Single binary, CRI-compatible, Offline-First, No Magic.
*   **Source of Truth:** Local directory with YAML files.

## Technical Architecture
The architecture consists of three logical layers:

1.  **Input Watcher:**
    *   Watches `/etc/epr/manifests/` (configurable) for filesystem changes.
    *   Detects additions/removals/updates of `apps/v1.Deployment`, `v1.ConfigMap`, and `v1.Secret`.

2.  **Engine & State Store:**
    *   **Engine:** Orchestrates the reconciliation process. It receives events from the Watcher and coordinates with the CRI Client.
    *   **Store:** In-memory thread-safe storage tracking the "Desired State" (from manifests) and "Actual State" (from CRI).
    *   **Debug API:** Exposes the internal state via a simple HTTP API (`/status`).

3.  **Container Lifecycle Manager (CRI):**
    *   Direct gRPC client for `containerd` or `CRI-O`.
    *   Maintains a sync loop to reconcile running containers with the local manifests.

## Current Status
**Phase:** Implementation (Phase 2)
*   **Source Code:**
    *   `cmd/kube-less`: Main entry point with configuration and signal handling.
    *   `internal/config`: Configuration loading.
    *   `internal/manifest`: Watcher (fsnotify) and Parser (Kubernetes schemes).
    *   `internal/runtime`: CRI gRPC client (connection established).
    *   `internal/engine`: Core logic, State Store, and Reconciliation Loop foundation.
    *   `internal/api`: Debug API server.
*   **Functionality:**
    *   Connects to CRI socket (successfully tested with Colima/Containerd).
    *   Watches and parses manifests from configured directories.
    *   Maintains internal state of Workloads.
    *   Exposes state via HTTP GET `/status`.
*   **Next Step:** Implement the active reconciliation logic to launch Pods based on the parsed Deployments.

## Development Roadmap (MVP)
*   **Phase 1:** Establish connection to the CRI socket and implement listing of running pods. [COMPLETED]
*   **Phase 2:** Parse a local YAML Deployment file and successfully launch a Pod (e.g., Nginx). [IN PROGRESS]
    *   *Next:* Implement `RunPodSandbox`, `CreateContainer`, `StartContainer` in the Engine.
*   **Phase 3:** Implement ConfigMap injection mechanisms.

## Technology Stack
*   **Language:** Go (Golang)
*   **Interface:** CRI (Container Runtime Interface) via gRPC
*   **Runtime Support:** `containerd`, `CRI-O`