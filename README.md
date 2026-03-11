# kube-less
K8s manifest runner without kubernetes :-)

**kube-less** (EdgePod Runner) is a lightweight agent that runs Kubernetes manifests (Deployments, ConfigMaps, Secrets) directly on a container runtime (containerd/CRI-O) via the CRI interface, without needing a full Kubernetes control plane.

## Architecture

- **Watcher:** Monitors a local directory for YAML changes.
- **Engine:** Maintains the desired state and orchestrates the reconciliation.
- **CRI Client:** Communicates directly with the Container Runtime Interface.
- **API:** Provides a simple status endpoint for debugging.

## Installation on Linux

### Dependencies

kube-less requires **containerd** and **CNI plugins**. Install them first:

#### Debian / Ubuntu

```bash
# containerd
sudo apt-get update
sudo apt-get install -y containerd

# CNI plugins
sudo apt-get install -y containernetworking-plugins

# Alternatively install CNI plugins manually (if package is unavailable):
CNI_VERSION=v1.5.1
sudo mkdir -p /opt/cni/bin
curl -sSL https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz \
  | sudo tar -xz -C /opt/cni/bin
```

#### Fedora / RHEL / CentOS Stream

```bash
# containerd
sudo dnf install -y containerd

# CNI plugins
sudo dnf install -y containernetworking-plugins

# Enable and start containerd
sudo systemctl enable --now containerd
```

#### openSUSE / SUSE Linux Enterprise

```bash
# containerd
sudo zypper install -y containerd

# CNI plugins
sudo zypper install -y containernetworking-plugins

# Enable and start containerd
sudo systemctl enable --now containerd
```

### CNI Configuration

After installing CNI plugins, create a CNI conflist so containerd knows which network plugin to use:

```bash
sudo mkdir -p /etc/cni/net.d

cat <<EOF | sudo tee /etc/cni/net.d/10-kube-less.conflist
{
  "cniVersion": "1.0.0",
  "name": "kube-less",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "kube-less0",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "subnet": "10.88.0.0/24",
        "routes": [{"dst": "0.0.0.0/0"}]
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
EOF
```

> **Multi-node setup:** Each node must use a unique subnet (e.g. `10.88.0.0/24`, `10.88.1.0/24`, …).
> Cross-node routing is handled at the infrastructure level (static routes, BGP) – outside kube-less scope.

### Install kube-less

```bash
# Build from source
git clone https://github.com/theztd/kube-less
cd kube-less
go build -o kube-less ./cmd/kube-less

sudo install -m 755 kube-less /usr/local/bin/kube-less
sudo mkdir -p /etc/kube-less /var/lib/kube-less

sudo cp configs/config.yaml /etc/kube-less/config.yaml
# Edit /etc/kube-less/config.yaml to match your environment
```

### systemd service

```ini
# /etc/systemd/system/kube-less.service
[Unit]
Description=kube-less agent
After=containerd.service
Requires=containerd.service

[Service]
ExecStart=/usr/local/bin/kube-less -config /etc/kube-less/config.yaml
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now kube-less
```

---

## Usage

### Start the agent

```bash
kube-less -config /etc/kube-less/config.yaml
```

### Check environment (dry-run)

> **TODO:** `check` subcommand is not yet implemented.

```bash
# Planned: verify CNI binaries, CNI config, and validate all manifests
# without starting the agent. Exits with code 0 on success, 1 on error.
kube-less check -config /etc/kube-less/config.yaml
```

---

## Configuration

```yaml
manifest_dirs:
  - "./examples/manifests"
cri_socket_path: "unix:///var/run/containerd/containerd.sock"
sync_interval: "5s"
debug_api_port: 8080
data_dir: /var/lib/kube-less
network:
  node_subnet: "10.88.0.0/24"
  bridge_name: "kube-less0"
  cni_conf_dir: "/etc/cni/net.d"
  cni_bin_dir:  "/opt/cni/bin"
```

### Running (development)

```bash
go run cmd/kube-less/main.go -config configs/config.yaml
```

### Debugging

Check the status of managed workloads:

```bash
curl localhost:8080/status
```

---

## Supported Resources (MVP)

- `apps/v1/Deployment` (Basic support)
- `v1/ConfigMap`
- `v1/Secret`
