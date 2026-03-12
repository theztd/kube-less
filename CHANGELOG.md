# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.0] – 2026-03-12

### Added

**Milestone A – Core Engine**
- Watcher: monitors local directories for YAML manifest changes (inotify-based)
- Engine: reconciliation loop with configurable `sync_interval`
- In-memory Store with thread-safe workload state tracking
- CRI client wrapping containerd gRPC API (sandbox + container lifecycle)

**Milestone B – Networking**
- CNI bridge network integration (`kube-less0` bridge, host-local IPAM)
- Sandbox IP discovery after pod creation
- Automatic CNI conflist generation if absent
- `GET /status` HTTP debug endpoint

**Milestone C – ConfigMap & Secret injection**
- `v1/ConfigMap` and `v1/Secret` resource parsing from YAML manifests
- Environment variable injection (`valueFrom.configMapKeyRef`, `valueFrom.secretKeyRef`)
- Volume mounts from ConfigMaps (files written to `dataDir` and bind-mounted)
- Content-hash-based rolling restarts when ConfigMap/Secret values change

**Milestone D – Readiness probes & endpoint discovery**
- HTTP GET readiness probe runner with `initialDelaySeconds`, `periodSeconds`,
  `successThreshold`, `failureThreshold`
- Named port resolution in probe `httpGet.port`
- Workloads without `readinessProbe` are marked ready immediately
- `GET /endpoints` HTTP endpoint listing only ready workloads with their IPs and ports

**Milestone E – Finalization**
- Exported godoc comments on all public types and methods
- Full nginx example manifest with ConfigMap, Secret, volume mount, and readinessProbe
- README updated with Architecture, Readiness Probes and `/endpoints` sections

**Milestone F – Developer experience & release pipeline**
- `Makefile` with `build`, `test`, `vet`, `lint`, `dist`, `fmt`, `docs`, `help` targets
- Version, commit SHA, and build date embedded via `-ldflags` at build time
- GitHub Actions CI workflow (vet + test + build on every push/PR)
- GitHub Actions release workflow via GoReleaser (linux/amd64 + linux/arm64 on `v*` tag)
- `scripts/prerelease.sh` – generates `docs/GODOC.md` from `go doc` (single-page reference)
- `doc.go` package overview files for all internal packages
- `.goreleaser.yml` for reproducible multi-arch release archives

### Known Limitations

- No liveness probe support (planned for v0.2.0)
- `replicas > 1` not supported – exactly one sandbox per Deployment
- `kube-less check` dry-run subcommand not yet implemented
- No `v1/Service` resource type – service discovery is currently only via `/endpoints`

---

## [Unreleased]

<!-- Next changes go here -->
