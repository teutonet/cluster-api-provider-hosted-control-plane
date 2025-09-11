# Changelog

## 1.0.0 (2025-09-11)


### Features

* add ExternalManagedControlPlane field and enhance resource reconciliation ([b4b5094](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4b50941242c77dc98a14890e9307ed34f959f7a))
* add HostedControlPlaneTemplate CRD and Kubernetes deployment manifests ([12123f3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/12123f30b01c3631fd0603e4c32a6a78aa08534c))
* add network policy support with component-based access control ([6475139](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6475139725c04e06f81e2cb9d5a5c4083fec7811))
* add NetworkPolicies ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
* add OpenTelemetry tracing to reconciler checksum and deployment operations ([4652889](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/465288956d142da3536a6a8ae0fc6a06f395ffdc))
* add project information ([06d806c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/06d806cd650b169a6e522736c8c4c63ac2471ec3))
* bootstrap Cluster API hosted control plane provider ([b4934f7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4934f7dca85e7640fb60cb17514f398dbfbaf47))
* **controller:** implement core HostedControlPlane service reconciliation ([09fa27c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/09fa27caa22b5c737dfbf6dd2766f5e99702237c))
* create etcd snapshot ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
* **deployment:** enhance control plane component deployment logic ([0601374](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/06013746a7fb03fef2c37b6a1b7d0b0a40adb850))
* **etcd:** implement ETCD cluster management and enhance certificates ([1b4b1cc](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/1b4b1ccb81a66b1976291139d315283ca4a5580f))
* expand certificate management and add resource checksumming ([7b1cfb8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/7b1cfb83e5e6a6348738715a2a6e10c87d96b307))
* extend API server configuration with mount support and workload cluster improvements ([a10410b](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a10410bff7e798d4b9f319806d5266cc8b3fedcc))
* extract workload config reconciliation into dedicated reconciler ([478a5f5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/478a5f551e2dbc775793d38f16ac1331ecbbd1a6))
* refactor workload reconciler to use dedicated RBAC and konnectivity reconcilers ([7458973](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74589732a47a64143ea9654b050dfc5181386e77))
* setup building and CI ([cdfa51d](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/cdfa51de89a5af39c6a1cd1fe9f931823e32dfee))
* update dependencies and use etcd client version for image ([aa7e3a7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa7e3a7a8d508477037966be2b9ca325518a099e))
* use helper function for getting IPs ([1f12e1c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/1f12e1cea6fbc4c610c416ad2150c92d4da6c347))
