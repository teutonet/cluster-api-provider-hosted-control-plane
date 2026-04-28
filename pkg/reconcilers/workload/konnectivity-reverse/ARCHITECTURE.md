# Reverse Konnectivity Architecture

## Problem Statement

Current konnectivity implementation is unidirectional:
- Control plane runs **server** (passive listener)
- Workload cluster runs **agent** (active connector)
- Enables: CP → WL communication only

For bidirectional support, workload cluster needs to reach control plane through konnectivity.

## Solution: Dual Tunnel Architecture

### Overview

Deploy a second, independent konnectivity tunnel:
- **Forward tunnel** (existing): CP server ←→ WL agent
- **Reverse tunnel** (new): WL server ←→ CP agent/client

This provides symmetric, bidirectional communication.

### Component Layout

#### Control Plane

**Forward:** Konnectivity server in API server pod
- Listens on port 8132 (agent connections)
- Active, managed by existing reconciler

**Reverse:** Konnectivity client/dialer in API server pod
- Connects to workload konnectivity server
- New sidecar or integrated container
- Uses kubeconfig to reach workload cluster

#### Workload Cluster

**Forward:** Konnectivity agent on each node (existing)
- Connects to CP server on port 8132
- Managed by existing reconciler

**Reverse:** Konnectivity server as sidecar/service (new)
- Main server/gRPC port: 8132 (accepts connections from CP)
- Admin port: 8133
- Health port: 8134
- Accepts connections from CP
- New DaemonSet/StatefulSet or pod sidecar
- Manifests/services/probes must target the correct port by purpose to avoid collisions

### RBAC & Authentication

#### Forward Tunnel (Existing)
- Agent SA: `konnectivity-agent`
- Gets token from projected volume
- Server validates via `authentication-audience`

#### Reverse Tunnel (New)
- **Workload server:**
  - New SA: `konnectivity-server` or extend existing
  - Needs: watch leases, get configmaps (for config)
  - Creates token for CP client authentication

- **CP client:**
  - Runs in CP, authenticates as workload cluster client
  - Uses workload kubeconfig with client cert
  - Token issued by workload cluster

### Network Policies

#### Forward (Existing)
```
workload agent → CP server (8132/tcp)
```

#### Reverse (New)
```
CP client → workload server (8134/tcp or custom port)
```

Both need explicit ingress rules to pass Cilium/NetworkPolicy.

### Data Flow

**Control Plane → Workload** (forward):
```
CP App → CP Konnectivity Server → (gRPC tunnel) → WL Agent → WL App
```

**Workload → Control Plane** (reverse):
```
WL App → WL Konnectivity Server ← (gRPC tunnel) ← CP Konnectivity Client ← CP App
```

## Implementation Phases

### Phase 1: API Types (CWRA-9)
- Add `KonnectivityReverse` spec to HostedControlPlane
- Configure: enabled flag, server port, resources, args

### Phase 2: Reconciliation (CWRA-7)
- Implement `ReverseKonnectivityReconciler`
- Mirror existing reconciler structure:
  - Phase 1: RBAC (SA, roles, bindings for workload server)
  - Phase 2: Deployment (server sidecar or pod in workload)
  - Phase 3: CP client configuration (in API server)

### Phase 3: Integration
- Update API server reconciler to add reverse konnectivity container/config
- Update workload konnectivity reconciler to add server capability
- Network policy updates

### Phase 4: Testing (CWRA-8)
- Unit tests for reconciliation logic
- Integration tests with real konnectivity setup
- E2E workload → CP connectivity validation

## Configuration Example

```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1alpha1
kind: HostedControlPlane
metadata:
  name: my-hcp
spec:
  konnectivityReverse:
    enabled: true
    serverPort: 8134
    server:
      image: "k8s.gcr.io/kas-network-proxy:v0.X.Y"
      replicas: 2
      resources:
        requests:
          cpu: 100m
          memory: 128Mi
    client:
      image: "k8s.gcr.io/kas-network-proxy/proxy-agent:v0.X.Y"
      resources:
        requests:
          cpu: 50m
          memory: 64Mi
```

## Security Considerations

1. **Token Exchange**: Both directions use service account tokens, validated via audience claim
2. **TLS**: gRPC tunnel encrypted, certs from existing cert-manager setup
3. **RBAC**: Minimal permissions, separate service accounts per direction
4. **Network**: Explicit allow-list policies, no open proxying
5. **DoS**: Konnectivity has connection limits, lease tracking prevents resource exhaustion

## Operational Notes

- Reverse tunnel failure does NOT impact forward tunnel
- Can be disabled/enabled per HCP without affecting CP
- Requires network path from CP to workload cluster (firewall rules)
- Certificate rotation handled by existing cert-manager integration
- Monitoring: health checks on both server and client sides

## Open Questions (from CWRA-6 research)

1. Does konnectivity native support bidirectional? (Research needed)
2. What konnectivity versions support this feature?
3. Do we need separate server instances or can one handle both?
4. Performance implications of dual tunnels vs. single bidirectional?
5. How to handle CP client failover if multiple CP replicas?

## Next Steps

1. Review CWRA-6 findings for konnectivity capabilities
2. Implement API types in HostedControlPlane
3. Create reverse reconciler following forward tunnel pattern
4. Add network policy updates
5. Comprehensive test suite
