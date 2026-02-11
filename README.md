# 🏗️ Cluster API Control Plane Provider for Hosted Control Planes

[![Go Report Card](https://goreportcard.com/badge/github.com/teutonet/cluster-api-provider-hosted-control-plane)](https://goreportcard.com/report/github.com/teutonet/cluster-api-provider-hosted-control-plane)
[![License](https://img.shields.io/badge/license-AGPL%20v3-blue.svg)](LICENSE)
[![CLA assistant](https://cla-assistant.io/readme/badge/teutonet/cluster-api-provider-hosted-control-plane)](https://cla-assistant.io/teutonet/cluster-api-provider-hosted-control-plane)

A Kubernetes Cluster API control plane provider that enables management of hosted control planes as first-class
Kubernetes resources. This provider allows you to create and manage highly available Kubernetes control plane components
(API Server, Controller Manager, Scheduler, and etcd) as hosted services, decoupling them from the underlying
infrastructure.

## 🌟 What is a Hosted Control Plane?

Traditional Kubernetes clusters tightly couple the control plane components with worker nodes on the same
infrastructure.
Hosted control planes break this coupling by running control plane components as managed services, offering:

- **Infrastructure Independence**: Control planes run separately from worker nodes
- **Enhanced Reliability**: Dedicated infrastructure for control plane components
- **Simplified Operations**: Automated lifecycle management and upgrades
- **Cost Optimization**: Shared control plane infrastructure across multiple clusters
- **Faster Provisioning**: Pre-provisioned control planes reduce cluster creation time

## 🚀 Key Features

### 🎯 Core Capabilities

- **Multi-Replica Control Plane**: Horizontal scaling for high availability
- **Automated ETCD Management**: Built-in backup/restore with S3 integration
- **Gateway API Integration**: Modern traffic routing and load balancing
- **Certificate Management**: Automated TLS via cert-manager integration
- **OpenTelemetry Observability**: Comprehensive tracing and monitoring
- **Cloud-Native Storage**: S3-compatible backup solutions

### 🔧 Advanced Features

- **Network Policy Support**: Component-based access control with automatic policy generation
  - When [Cilium](https://cilium.io/) is detected, Cilium Network Policies are created instead of vanilla Kubernetes
    Network Policies (recommended)
  - Cilium policies provide enhanced capabilities such as DNS-based filtering, allowing workload pods to communicate
    only with their specific control plane endpoints
  - Falls back to vanilla Kubernetes Network Policies when Cilium is not available

## 🏛️ Architecture

```mermaid
graph TB
    subgraph MC["🏗️ Management Cluster"]
        subgraph HCP["🎛️ Hosted Control Plane"]
            direction TB
            subgraph CP["Control Plane Components"]
                direction LR
                API["📡 API Server"]
                CM["🔧 Controller Manager"]
                SCHED["⏰ Scheduler"]
                API --- CM --- SCHED
            end

            ETCD["💾 ETCD Cluster"]
            CP --- ETCD
        end

        CAPI["🤖 Cluster API"]
        PROVIDER["🏠 HCP Provider"]
    end

    subgraph WC["🔨 Workload Cluster"]
        direction LR
        W1["👷 Worker Node 1"]
        W2["👷 Worker Node 2"]
        WN["👷 Worker Node N"]
        W1 --- W2 --- WN
    end

    HCP -->|"🔑 Kubeconfig"| WC
    CAPI --> PROVIDER
    PROVIDER --> HCP

    style MC fill:#e1f5fe,color:#000
    style HCP fill:#f3e5f5,color:#000
    style WC fill:#e8f5e8,color:#000
    style API fill:#fff3e0,color:#000
    style CM fill:#fff3e0,color:#000
    style SCHED fill:#fff3e0,color:#000
    style ETCD fill:#fce4ec,color:#000
```

## 🛠️ Installation

### Prerequisites

- Kubernetes cluster (management cluster) v1.28+
- Cluster API v1.11+ installed
- cert-manager v1.18+ for certificate management
- Gateway API v1.3+ CRDs installed
- A Gateway with a TLS wildcard listener (see [Gateway and DNS Configuration](#gateway-and-dns-configuration))

### Gateway and DNS Configuration

The provider requires a Gateway with a **TLS listener using a wildcard hostname**. This is used to route traffic to
hosted control planes.

#### Gateway Setup

Create a Gateway with a TLS passthrough listener:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: capi
  namespace: capi-system
spec:
  gatewayClassName: your-gateway-class # e.g., cilium, envoy, etc.
  listeners:
    - name: tls-passthrough
      protocol: TLS
      # this is the exposed port, the number here might be different
      # depending on your gateway-api provider
      port: 443
      hostname: "*.clusters.example.com" # Must be a wildcard
      tls:
        mode: Passthrough
      allowedRoutes:
        namespaces:
          from: All
```

**Important**: The listener **must** use:

- `protocol: TLS` (not HTTPS)
- A wildcard hostname (e.g., `*.clusters.example.com`)
- `tls.mode: Passthrough` to allow the API server to terminate TLS

#### DNS Configuration

Configure DNS to resolve the wildcard domain to your Gateway's external IP/hostname.

Each cluster gets an endpoint derived from the wildcard:

```
<cluster-name>.<cluster-namespace>.<wildcard-domain>
```

For example, with hostname `*.clusters.example.com`:

- Cluster `prod` in namespace `default` → `prod.default.clusters.example.com`
- Cluster `staging` in namespace `team-a` → `staging.team-a.clusters.example.com`

**Konnectivity subdomain**: The Konnectivity server (used for node-to-control-plane communication) requires an
additional `konnectivity.` prefix:

```
konnectivity.<cluster-name>.<cluster-namespace>.<wildcard-domain>
```

This means your DNS setup must handle requests like `konnectivity.prod.default.clusters.example.com`. Depending on your
DNS provider, you may need:

- A deeper wildcard record that matches the three-label prefix, for example: `*.*.*.clusters.example.com`
- Or explicit DNS records for each required Konnectivity hostname

### cert-manager Configuration

cert-manager must be configured to enable secret owner reference propagation. This ensures that certificate secrets are
properly garbage collected when the owning Certificate resource is deleted.

For more details, see the [cert-manager documentation on secret owner references](https://cert-manager.io/docs/usage/certificate/#cleaning-up-secrets-when-certificates-are-deleted).

### Install the Provider

```bash
# Install using the latest release
kubectl apply -f https://github.com/teutonet/cluster-api-provider-hosted-control-plane/releases/latest/download/control-plane-components.yaml

# Or install a specific version
kubectl apply -f https://github.com/teutonet/cluster-api-provider-hosted-control-plane/releases/download/v1.5.0/control-plane-components.yaml
```

### Verify Installation

```bash
kubectl get pods -n capi-hosted-control-plane-system
```

## 📖 Usage

### Basic Hosted Control Plane

```yaml
apiVersion: controlplane.cluster.x-k8s.io/v1alpha1
kind: HostedControlPlane
metadata:
  name: my-hosted-control-plane
  namespace: default
spec:
  version: v1.33.0
  replicas: 3
  gateway:
    # Reference to the Gateway with a TLS wildcard listener
    # See "Gateway and DNS Configuration" section above
    name: capi
    namespace: capi-system
```

### Integration with Cluster API

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: my-cluster
spec:
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1alpha1
    kind: HostedControlPlane
    name: my-hosted-control-plane
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
    kind: AWSCluster # Or your infrastructure provider
    name: my-aws-cluster
```

## 🎛️ Configuration

### Environment Variables

| Variable                      | Description                      | Default                               |
| ----------------------------- | -------------------------------- | ------------------------------------- |
| `LEADER_ELECTION`             | Enable leader election           | true                                  |
| `WEBHOOK_CERT_DIR`            | Directory for webhook certs      | /tmp/k8s-webhook-server/serving-certs |
| `MAX_CONCURRENT_RECONCILES`   | Max concurrent reconciles        | 10                                    |
| `CONTROLLER_NAMESPACE`        | Namespace for the controller     |                                       |
| `LOG_FORMAT`                  | Log format (json or text)        | json                                  |
| `LOG_LEVEL`                   | Log level (debug, info, etc.)    | info                                  |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OpenTelemetry collector endpoint | -                                     |

## 🧪 Development

### Prerequisites

- Go 1.25+
- kubectl
- [Task](https://taskfile.dev) (for building and testing)

### Build and Test

```bash
# Build the project
task build

# Run tests
task test

# Run linting
task lint

# Generate manifests
task manifests

# Full CI pipeline
task ci
```

### Local Development

```bash
# have CAPI installed
task manifests
kubectl apply -f ./build/control-plane-components.yaml
task dev
# Run the controller locally within you IDE (.run for IDEA provided)
```

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md)
for details on:

- Code of Conduct
- Development process
- Pull request guidelines
- Issue reporting

### Development Workflow

1. Fork the repository
2. Create a feature branch (`git switch -c feature/amazing-feature`)
3. Make your changes
4. Run tests and linting (`task ci`)
5. Commit your changes (`git commit`)
6. Push to the branch (`git push fork feature/amazing-feature`)
7. Open a Pull Request (`gh pr create` is amazing!)

## 📋 Compatibility

| Component    | Version |
| ------------ | ------- |
| Kubernetes   | v1.28+  |
| Cluster API  | v1.11+  |
| cert-manager | v1.18+  |
| Gateway API  | v1.3+   |
| Go           | 1.25+   |

## 🛣️ Roadmap

- [ ] **Enhanced Monitoring**: Prometheus metrics
- [ ] **Auto-Scaling**: Dynamic control plane scaling based on load

## 🆘 Support

- **GitHub Issues**: [Report bugs or request features](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues)
- **Discussions**: [Community discussions and Q&A](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/discussions)

## 📄 License

This project is licensed under the GNU Affero General Public License v3.0 -
see the [LICENSE](LICENSE) file for details.

---

<div align="center">

**Built with ❤️ by the [teuto.net](https://teuto.net) team**

[⭐ Star us on GitHub](https://github.com/teutonet/cluster-api-provider-hosted-control-plane) • [🐛 Report Issues](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues) • [💬 Join Discussions](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/discussions)

</div>
