# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Cluster API control plane provider for hosted control planes (HCP), enabling management of Kubernetes control
plane components as hosted services. The project implements a custom controller that manages the lifecycle of hosted
control planes including API server, controller manager, scheduler, and etcd components.

## Common Development Commands

This project uses [Task](https://taskfile.dev) as the build system. Key commands:

- `task` or `task build` - Build the project with multi-architecture support and image creation
- `task test` - Run all tests with coverage output to `_artifacts/cover.out`
- `task test path=<pkg>` - Run tests for specific package (e.g., `task test path=pkg/hostedcontrolplane`)
- `task lint` - Run golangci-lint with formatting and linting checks
- `task lint fix=true` - Run linting with automatic fixes
- `task format` - Format code using gofumpt and golines
- `task generate` - Generate deepcopy and conversion methods
- `task manifests` - Generate Kubernetes manifests (CRDs, RBAC, webhooks) using controller-gen and kustomize
- `task ci` - Run full CI pipeline (lint + test)
- `task clean` - Clean build and artifact directories
- `task check-diff` - Verify no uncommitted changes after generation
- `task compile` - Compile binaries for specific architectures
- `task tidy` - Run go mod tidy
- `task get-version` - Get the current version
- `task dev` - Local development with remote Kubernetes clusters using telepresence

## Architecture

### Core Components

- **API Types** (`api/v1alpha1/`): Custom resource definitions for HostedControlPlane and HostedControlPlaneTemplate
- **Controller** (`pkg/hostedcontrolplane/controller.go`): Main reconciliation logic for hosted control plane lifecycle
- **Reconcilers** (`pkg/reconcilers/`): Specialized reconcilers for different components:
    - `etcd_cluster/`: ETCD cluster management with backup/restore capabilities
    - `workload/`: Workload cluster components (RBAC, CoreDNS, kube-proxy)
    - `kubeconfig/`: Kubeconfig generation and management for cluster access
    - `certificates/`: Certificate management via cert-manager
    - `tlsroutes/`: Gateway API TLS route configuration
    - `infrastructure_cluster/`: Infrastructure cluster setup
    - `apiserverresources/`: API server service and deployment management
    - `alias/`: Type aliases for workload cluster clients
- **Operator** (`pkg/operator/`): Controller manager setup and configuration
- **Utilities** (`pkg/util/`): Common utilities for errors, logging, tracing

### Key Features

- **Multi-replica Control Plane**: Supports scaling control plane components
- **ETCD Management**: Includes backup/restore functionality with S3 storage
- **Gateway Integration**: Uses Gateway API for traffic routing
- **Certificate Management**: Integrates with cert-manager for TLS
- **Observability**: OpenTelemetry tracing integration
- **Cloud Integration**: S3 support for ETCD backups

## Code Style and Tools

- **Linting**: Uses golangci-lint with extensive rule set (see `.golangci.yaml`)
- **Formatting**: gofumpt + golines (120 char limit)
- **Import Aliases**: Strict import alias rules enforced (see `.golangci.yaml` importas section)
- **Generated Code**: Controller-gen for CRDs, conversion-gen for API conversions

## Dependency Management

- `k8s.io/kubernetes`'s own `go.mod` requires all of its staging modules (`k8s.io/api`, `k8s.io/apiserver`,
  `k8s.io/client-go`, `k8s.io/component-helpers`, `k8s.io/cri-client`, ...) at version `v0.0.0`, and replaces them
  with `=> ./staging/src/k8s.io/...` local paths. Go ignores `replace` directives from a dependency's `go.mod` when
  that dependency is imported by another module, so consumers of `k8s.io/kubernetes` see plain `v0.0.0` requirements
  with no real module version behind them. If nothing else in the module graph provides a real version for one of
  these paths, resolving a package that imports `k8s.io/kubernetes` fails with `reading .../go.mod at revision
  v0.0.0: unknown revision v0.0.0` (reproduced in a minimal module that only imports
  `k8s.io/kubernetes/pkg/features`).
- Only pin k8s.io staging modules this repo actually imports (check with `go list -deps ./... | grep k8s.io`). Don't
  add entries for staging modules this repo doesn't import (`k8s.io/cloud-provider`, `k8s.io/kube-scheduler`,
  `k8s.io/mount-utils`, etc., per the copy-pasted lists in upstream examples) — `go mod tidy` never needs them and
  they're dead weight.
- Of the ones we do need, some are imported directly by our code (`k8s.io/api`, `k8s.io/client-go`, etc.) and are
  pinned with a plain top-level `require`, same as any other dependency. The rest (`k8s.io/cli-runtime`,
  `k8s.io/component-helpers`, `k8s.io/controller-manager`, `k8s.io/cri-api`, `k8s.io/cri-client`) are only pulled in
  transitively through `k8s.io/kubernetes`, and exist in `go.mod` purely as a workaround for the `v0.0.0` problem
  above, not because our code needs a say in their version — those get a `replace => vX` instead of a plain
  `require`, to make that distinction visible: `require` = "we import this", `replace` = "pinned only to patch
  upstream's broken self-reference". Leaving these 5 as plain `require` without an explicit version is not an
  option: nothing else in the graph provides a real version for `component-helpers` or `cri-client`, so `go mod
  tidy` hits the exact `unknown revision v0.0.0` error again (reproduced by deleting their `require` lines).
  `replace`'s hard MVS override for these 5 buys little in practice since Renovate (`.github/renovate.json`) has no
  grouping rule for k8s.io/* and will bump them one at a time regardless — any resulting incompatibility (e.g.
  `k8s.io/cri-client@v0.35.0` no longer implementing `k8s.io/cri-api@v0.36.2`'s interfaces, a real compile failure
  hit while testing this) is a hard compile error CI catches either way. The split is mainly for `go.mod`
  readability: it marks which modules are "we use this API" vs "upstream's own module graph needs this pinned".
- `sigs.k8s.io/cluster-api` has a similar split: since v1.14.0 its API types live in `sigs.k8s.io/cluster-api/api`,
  referenced via a local `replace sigs.k8s.io/cluster-api/api => ./api` in cluster-api's own `go.mod` that likewise
  doesn't propagate downstream. Fix here is a plain `require sigs.k8s.io/cluster-api/api <matching-version>` — it's
  a plain `require` because it's imported directly (`capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"`, used across
  most of `pkg/`), same as the direct k8s.io modules above. No `replace` needed either way here since it's a single
  module, not several siblings that can drift apart from each other and need a visual "pinned for a different
  reason" marker.
- When bumping `k8s.io/kubernetes` (or `sigs.k8s.io/cluster-api`), update the version of every `require`/`replace`
  line for its staging/split modules to match, and re-run `go mod tidy`.

## Testing

- Test files follow `*_test.go` convention
- Use `task test` to run all tests or `task test path=<package>` for specific packages
- Testing frameworks: Uses standard Go testing with gomega for assertions

## Build and Artifacts

- **Build Directory**: `build/` - Contains compiled binaries and generated manifests
- **Artifacts Directory**: `_artifacts/` - Contains test coverage reports and linting output
- **Multi-Architecture**: Supports amd64 and arm64 architectures
- **Container Images**: Automatically built during the build process
- **Manifests**: Generated using controller-gen and assembled with kustomize

## Development Environment

The project includes a `task dev` (telepresence) task for local development with remote Kubernetes clusters, allowing
local debugging while connected to a cluster environment. This enables running the controller locally while it interacts
with a remote Kubernetes cluster.
