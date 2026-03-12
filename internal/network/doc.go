// Package network manages CNI (Container Network Interface) configuration and
// sandbox IP discovery for kube-less.
//
// Manager reads or creates a CNI conflist for the bridge network, attaches
// the CNI network to freshly created pod sandboxes via the CNI plugin binaries,
// and queries the assigned sandbox IP from the containerd namespace. Each node
// should use a unique subnet so that cross-node routing (handled externally)
// can deliver packets between kube-less workloads on different machines.
package network
