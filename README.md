# kube-less
K8s manifest runner without kubernetes :-)

**kube-less** (EdgePod Runner) is a lightweight agent that runs Kubernetes manifests (Deployments, ConfigMaps, Secrets) directly on a container runtime (containerd/CRI-O) via the CRI interface, without needing a full Kubernetes control plane.

## Architecture

- **Watcher:** Monitors a local directory for YAML changes.
- **Engine:** Maintains the desired state and orchestrates the reconciliation.
- **CRI Client:** Communicates directly with the Container Runtime Interface.
- **API:** Provides a simple status endpoint for debugging.

## Getting Started

### Prerequisites

- Go 1.23+
- A running container runtime with CRI support (e.g., `containerd` via Colima or directly installed).

### Configuration

Copy the example configuration:

```yaml
manifest_dirs:
  - "./examples/manifests"
cri_socket_path: "unix:///var/run/containerd/containerd.sock" # Adjust for your system
sync_interval: "5s"
debug_api_port: 8080
```

### Running

```bash
go run cmd/kube-less/main.go -config configs/config.yaml
```

### Debugging

Check the status of managed workloads:

```bash
curl localhost:8080/status
```

## Supported Resources (MVP)

- `apps/v1/Deployment` (Basic support)
- `v1/ConfigMap`
- `v1/Secret`