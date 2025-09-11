# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Cluster API control plane provider for hosted control planes (HCP), enabling management of Kubernetes control plane components as hosted services. The project implements a custom controller that manages the lifecycle of hosted control planes including API server, controller manager, scheduler, and etcd components.

## Common Development Commands

This project uses [Task](https://taskfile.dev) as the build system. Key commands:

- `task` or `task build` - Build the project (creates `build/manager` binary)
- `task test` - Run all tests with coverage output to `_artifacts/cover.out`
- `task lint` - Run golangci-lint with formatting and linting checks
- `task lint fix=true` - Run linting with automatic fixes
- `task format` - Format code using gofumpt and golines
- `task generate` - Generate deepcopy and conversion methods
- `task manifests` - Generate Kubernetes manifests (CRDs, RBAC, webhooks) using controller-gen
- `task ci` - Run full CI pipeline (lint + test)
- `task clean` - Clean build and artifact directories
- `task check-diff` - Verify no uncommitted changes after generation

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

## Testing

- Test files follow `*_test.go` convention

## Development Environment

The project includes a `dev` task for local development with remote Kubernetes clusters, allowing local debugging while connected to a cluster environment.
