# Changelog

## [0.2.0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/cluster-api-control-plane-provider-hcp-v0.1.0...cluster-api-control-plane-provider-hcp-v0.2.0) (2025-09-23)


### Features

* add auditConfiguration option ([e5e78d5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/e5e78d5a60dd94c5ff882ef7c3de70b004e73ae4))
* add authentication for audit webhooks ([#11](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/11)) ([0a64b9c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/0a64b9cd8bf477c32a55ae922144ae65e3da61f7))
* add ExternalManagedControlPlane field and enhance resource reconciliation ([b4b5094](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4b50941242c77dc98a14890e9307ed34f959f7a))
* add HostedControlPlaneTemplate CRD and Kubernetes deployment manifests ([12123f3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/12123f30b01c3631fd0603e4c32a6a78aa08534c))
* add more tests ([f18cd8a](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/f18cd8a82dc7145db10d9ba10c2eea4280b4c8bd))
* add network policy support with component-based access control ([6475139](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6475139725c04e06f81e2cb9d5a5c4083fec7811))
* add NetworkPolicies ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
* add OpenTelemetry tracing to reconciler checksum and deployment operations ([4652889](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/465288956d142da3536a6a8ae0fc6a06f395ffdc))
* add project information ([2622bc1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2622bc1922d6a7f839f3808b2439ff2d6503997a))
* add tests for util package ([27396c9](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27396c91084008671c223b438e37619ac8c5a6c4))
* allow overriding the images ([#5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/5)) ([cb9dd85](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/cb9dd850e681f4ce26a63fb132a869d37e7bfb43))
* allow referencing a secret from another namespace for audit webhook authentication ([#12](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/12)) ([aa563a2](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa563a21fbadffa5827591dd23a8d586435e646b))
* bootstrap Cluster API hosted control plane provider ([b4934f7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4934f7dca85e7640fb60cb17514f398dbfbaf47))
* compare with hardcoded `main` for default branch ([67a77d8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/67a77d8ec8f9db58d490721de9b01e00e4027045))
* **controller:** implement core HostedControlPlane service reconciliation ([09fa27c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/09fa27caa22b5c737dfbf6dd2766f5e99702237c))
* create etcd snapshot ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
* **deployment:** enhance control plane component deployment logic ([0601374](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/06013746a7fb03fef2c37b6a1b7d0b0a40adb850))
* **etcd:** implement ETCD cluster management and enhance certificates ([1b4b1cc](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/1b4b1ccb81a66b1976291139d315283ca4a5580f))
* expand certificate management and add resource checksumming ([7b1cfb8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/7b1cfb83e5e6a6348738715a2a6e10c87d96b307))
* extend API server configuration with mount support and workload cluster improvements ([a10410b](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a10410bff7e798d4b9f319806d5266cc8b3fedcc))
* extract workload config reconciliation into dedicated reconciler ([478a5f5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/478a5f551e2dbc775793d38f16ac1331ecbbd1a6))
* implement s3 backup ([d2dc916](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/d2dc9168d673a4a9e9b8ee75a7635334270e89b5))
* refactor workload reconciler to use dedicated RBAC and konnectivity reconcilers ([7458973](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74589732a47a64143ea9654b050dfc5181386e77))
* setup building and CI ([a3982ed](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a3982eda20b60fcfab7035e43d70e58a3b659263))
* update dependencies and use etcd client version for image ([aa7e3a7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa7e3a7a8d508477037966be2b9ca325518a099e))
* use helper function for getting IPs ([9bd07d3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/9bd07d308defdc246cfb965780b0ddf5f3f78562))
* Use mermaid instead of ASCII ([86e674e](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/86e674e19158f1554114d8b695e730671af9ce70))


### Bug Fixes

* **ci:** apparently there is no variable expansion here ([ebc0361](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/ebc0361ad7035684823128f9f59df2012e1b90a6))
* **ci:** missing release-please manifest ([284f6f4](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/284f6f47a4796629be50fcb5207ee4ddc3482193))
* code scanning alerts ([27d2866](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27d2866497cd7f6795e16f57762b2c4b4f9c6e64))
* implement resource deletion ([931b320](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/931b320e94f7ce8a66a01fed12cf003b963239f2))
* README formatting ([b3b3dea](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b3b3dea6de1f0d68a63fc2e40f5359cd76c38ae9))
