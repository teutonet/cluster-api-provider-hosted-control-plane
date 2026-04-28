# Reverse Konnectivity Implementation Plan

## Overview

Implement bidirectional konnectivity support by adding a reverse tunnel (workload → control plane).
This complements the existing forward tunnel (control plane → workload).

**Architecture**: Dual Tunnel Model
- Forward: CP server ↔ WL agent (existing)
- Reverse: WL server ↔ CP client (new)

## Implementation Sequence

### Step 1: API Types Extension (CWRA-9 Scaffolding)

**Files to modify:**
- `api/v1alpha1/hostedcontrolplane_types.go`

**Changes:**
```go
// Add to HostedControlPlaneSpec
KonnectivityReverse *KonnectivityReverse

// New type
type KonnectivityReverse struct {
    Enabled     bool
    Server      KonnectivityServerConfig
    Client      KonnectivityClientConfig
}

type KonnectivityServerConfig struct {
    Image       string
    Replicas    *int32
    Port        *int32
    Resources   *ResourceRequirements
    Args        map[string]string
}

type KonnectivityClientConfig struct {
    Image       string
    Resources   *ResourceRequirements
    Args        map[string]string
}
```

**Rationale**: Mirrors existing KonnectivityClient pattern, allows per-component configuration

### Step 2: Reconciler Integration (CWRA-7 Core)

#### 2.1 Reverse Konnectivity Reconciler

**File:** `pkg/reconcilers/workload/konnectivity-reverse/reconciler.go` (scaffolded)

**Implement:**

**Phase 1 - RBAC:**
```go
func reconcileReverseKonnectivityRBAC(ctx context.Context) error {
    // 1. Create ServiceAccount: konnectivity-server
    sa := corev1ac.ServiceAccount("konnectivity-server", namespace)
    
    // 2. Create Role with permissions:
    //    - watch, list leases (coordination.k8s.io)
    //    - get configmaps (for server config)
    //    - patch events (for logging)
    
    // 3. Create RoleBinding to bind SA to Role
    
    // 4. Create ClusterRoleBinding for system:auth-delegator
    //    (allows control plane to validate tokens)
}
```

**Phase 2 - Deployment:**
```go
func reconcileReverseKonnectivityDeployment(ctx context.Context, 
    hcp *v1alpha1.HostedControlPlane, cluster *capiv2.Cluster) error {
    
    // 1. Create DaemonSet or Deployment with konnectivity server
    // 2. Configure:
    //    - Port: from hcp.Spec.KonnectivityReverse.Server.Port
    //    - Mode: "grpc" (mirror of forward server)
    //    - Service account: konnectivity-server
    // 3. Mount projected token (for CP authentication)
    // 4. Health probes on server health port
    // 5. Network policies: allow CP → WL on server port
}
```

**Phase 3 - Network Policy:**
```go
func reconcileReverseKonnectivityNetworkPolicy(ctx context.Context,
    cluster *capiv2.Cluster) error {
    
    // Create Cilium/NetworkPolicy allowing:
    // - Source: control plane
    // - Destination: konnectivity-server pods
    // - Port: server.Port
}
```

#### 2.2 API Server Integration

**File:** `pkg/reconcilers/apiserverresources/apiserverresources.go`

**Modifications:**
```go
// In reconcileAPIServerDeployment:
if hcp.Spec.KonnectivityReverse != nil && hcp.Spec.KonnectivityReverse.Enabled {
    // Add reverse client container/sidecar to API server pod
    container := createKonnectivityReverseClientContainer(...)
    podSpec.Containers = append(podSpec.Containers, container)
    
    // Add kubeconfig volume for workload cluster access
    volumes := appendReverseKonnectivityVolumes(...)
}
```

**Container Configuration:**
- Image: from `KonnectivityReverse.Client.Image`
- Args: connect to workload reverse server, use client credentials
- Mount: workload kubeconfig, CA cert, reverse server token
- Health: readiness/liveness on client health port

### Step 3: Type Generation & Manifests

**Files to generate:**
- Run `task generate` to create deepcopy methods
- Run `task manifests` to update CRDs

**Verifications:**
- ✅ CRD includes new KonnectivityReverse field
- ✅ Validation rules in place (enable ⇒ server required)
- ✅ OpenAPI schema updated

### Step 4: Controller Wiring (CWRA-7 Integration)

**File:** `pkg/hostedcontrolplane/controller.go`

**Add reconciler initialization:**
```go
reverseReconciler := konnectivityreverse.NewReverseKonnectivityReconciler(
    workloadClient,
    ciliumClient,
    konnectivityNamespace,
    "konnectivity-server",
    serverAudience,
    reverseServerPort,
)
```

**Call in reconciliation loop:**
```go
if hcp.Spec.KonnectivityReverse != nil && hcp.Spec.KonnectivityReverse.Enabled {
    if reason, err := reverseReconciler.ReconcileReverseKonnectivity(
        ctx, hcp, cluster) {
        // Handle reason/error like other reconcilers
    }
}
```

### Step 5: Testing (CWRA-8)

**Test Coverage:**

1. **Unit Tests** (`reconciler_test.go`):
   - RBAC creation (SA, roles, bindings)
   - Deployment configuration
   - Network policy generation
   - Token mounting

2. **Integration Tests**:
   - Deploy HCP with reverse enabled
   - Verify workload server starts
   - Verify CP client connects
   - Test workload → CP communication

3. **E2E Tests**:
   - Create test pod in workload cluster
   - Have it reach service in control plane
   - Validate bidirectional traffic works

## Detailed Implementation Checklist

### CWRA-9 Scaffolding (DONE)
- [x] Architecture document created
- [x] Reconciler scaffolding (reconciler.go)
- [x] Implementation plan (this file)
- [ ] API types added to HostedControlPlane

### CWRA-7 Implementation
- [ ] API types fully implemented and validated
- [ ] Reverse reconciler RBAC phase implemented
- [ ] Reverse reconciler Deployment phase implemented
- [ ] Reverse reconciler NetworkPolicy phase implemented
- [ ] API server integration (add client container)
- [ ] Controller.go wiring
- [ ] Manifest generation

### CWRA-8 Testing
- [ ] Unit tests for all RBAC operations
- [ ] Unit tests for deployment generation
- [ ] Integration tests with real cluster
- [ ] E2E bidirectional connectivity test
- [ ] Error handling and edge case tests
- [ ] Documentation updates

## Key Design Decisions

### Why Dual Tunnel vs. Bidirectional Single?
- **Dual**: Independent, proven pattern, easier to debug, can be disabled
- **Single**: Simpler operationally, but requires konnectivity native support
- **Decision**: Dual tunnel (unless CWRA-6 research shows native support)

### Server Deployment Model
- **Options**: DaemonSet, Deployment, StatefulSet, Sidecar in konnectivity pod
- **Chosen**: Deployment with configurable replicas (mirrors agent design)
- **Rationale**: Easy to scale, independent from existing agent pods

### RBAC Separation
- Separate SA/roles for server vs. agent
- Least privilege: server only gets what it needs
- Clear audit trail for each direction

## Dependencies & Blockers

**On CWRA-6 Research:**
- Confirm konnectivity version support for dual tunnel
- Verify token validation works bidirectionally
- Performance characteristics (any known issues?)

**Infrastructure:**
- Network policy support (Cilium or vanilla)
- Certificate rotation (use existing cert-manager)
- Firewall rules: CP must be able to reach WL

## Rollout Plan

1. Feature branch: `feat/reverse-konnectivity`
2. Implementation in phases (RBAC → Deployment → Integration)
3. Test each phase before moving to next
4. Code review before merge
5. Optional feature (disabled by default)

## Monitoring & Observability

**Metrics to add:**
- `konnectivity_reverse_server_connections` (active connections)
- `konnectivity_reverse_client_connections` (CP side)
- `konnectivity_reverse_rpc_duration` (latency)

**Health checks:**
- Server readiness: health port response
- Client readiness: connection status
- Token expiry warnings

**Events:**
- Server pod started/failed
- Client connected/disconnected
- RBAC errors (helpful for debugging)

## Documentation Updates

1. Architecture diagram (forward + reverse tunnels)
2. Configuration examples (KonnectivityReverse spec)
3. Troubleshooting guide (common issues)
4. Network policy examples
5. Performance tuning (if needed)

## Success Criteria

- [ ] Workload cluster can initiate connections to control plane
- [ ] Bidirectional communication works reliably
- [ ] RBAC is properly configured
- [ ] Network policies allow necessary traffic
- [ ] Tests pass (unit, integration, E2E)
- [ ] Feature can be disabled without affecting forward tunnel
- [ ] Documentation complete
