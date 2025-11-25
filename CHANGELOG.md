# Changelog

## [1.5.0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/v1.4.0...v1.5.0) (2025-11-25)


### Features

* add auditConfiguration option ([e5e78d5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/e5e78d5a60dd94c5ff882ef7c3de70b004e73ae4))
* add authentication for audit webhooks ([#11](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/11)) ([0a64b9c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/0a64b9cd8bf477c32a55ae922144ae65e3da61f7))
* add CiliumNetworkPolicies ([6a9e1b0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6a9e1b006fe5cab74da5fef00ebcb90d72534ad2))
* add ExternalManagedControlPlane field and enhance resource reconciliation ([b4b5094](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4b50941242c77dc98a14890e9307ed34f959f7a))
* add hash for kube-root-ca.crt configmap to konnectivity-agent ([#38](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/38)) ([9f4e977](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/9f4e9777ff3c4b55206d62e683414dccf0045c3c))
* add HostedControlPlaneTemplate CRD and Kubernetes deployment manifests ([12123f3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/12123f30b01c3631fd0603e4c32a6a78aa08534c))
* add more tests ([f18cd8a](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/f18cd8a82dc7145db10d9ba10c2eea4280b4c8bd))
* add network policy support with component-based access control ([6475139](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6475139725c04e06f81e2cb9d5a5c4083fec7811))
* add NetworkPolicies ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
* add nix dev-shell ([#51](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/51)) ([d6c6eb1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/d6c6eb1eef3ee4f87a63a16875d865b211269546))
* add OpenTelemetry tracing to reconciler checksum and deployment operations ([4652889](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/465288956d142da3536a6a8ae0fc6a06f395ffdc))
* add project information ([2622bc1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2622bc1922d6a7f839f3808b2439ff2d6503997a))
* add tests for util package ([27396c9](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27396c91084008671c223b438e37619ac8c5a6c4))
* add whole lifecycle test ([#26](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/26)) ([2cb6170](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2cb617000197236f65314b68f413004beb6c9827))
* allow overriding the images ([#5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/5)) ([cb9dd85](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/cb9dd850e681f4ce26a63fb132a869d37e7bfb43))
* allow referencing a secret from another namespace for audit webhook authentication ([#12](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/12)) ([aa563a2](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa563a21fbadffa5827591dd23a8d586435e646b))
* auto-generate the metadata.yaml instead of manually adjusting it ([#42](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/42)) ([4a29346](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/4a2934667d62dca2fc1120396faf1e167246261c))
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
* switch konnectivity agent from DaemonSet to Deployment ([6a9e1b0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6a9e1b006fe5cab74da5fef00ebcb90d72534ad2))
* update dependencies and use etcd client version for image ([aa7e3a7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa7e3a7a8d508477037966be2b9ca325518a099e))
* update k8s dependencies + dependants ([#41](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/41)) ([885dcb5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/885dcb554d04fbb6c19f6186fe4cfee68022686e))
* use helper function for getting IPs ([9bd07d3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/9bd07d308defdc246cfb965780b0ddf5f3f78562))
* Use mermaid instead of ASCII ([86e674e](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/86e674e19158f1554114d8b695e730671af9ce70))


### Bug Fixes

* always create new titleCaser ([#37](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/37)) ([bafb5a1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/bafb5a138b2f9e0761550bf6fd8d3e2e945113e6))
* **ci:** apparently hidden in changelog means ignored completely ([0d1733e](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/0d1733eb6901230026d7ed4d6629d4d661e08141))
* **ci:** apparently there is no variable expansion here ([ebc0361](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/ebc0361ad7035684823128f9f59df2012e1b90a6))
* **ci:** missing release-please manifest ([284f6f4](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/284f6f47a4796629be50fcb5207ee4ddc3482193))
* code scanning alerts ([27d2866](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27d2866497cd7f6795e16f57762b2c4b4f9c6e64))
* create a new ctx with cancel for each call ([#33](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/33)) ([2d6f7b8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2d6f7b860d0b80336769694016784f7af7d522d7))
* don't set NODE_IP for kubeproxy, otherwise it fails without cloud provider ([#27](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/27)) ([609f3c7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/609f3c70f1f7702605ad9fec718a627b7af02437))
* implement resource deletion ([931b320](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/931b320e94f7ce8a66a01fed12cf003b963239f2))
* oidc jwks conformance ([#39](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/39)) ([5925f36](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/5925f361a4470d321743a1a85994da5ff147705b))
* README formatting ([b3b3dea](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b3b3dea6de1f0d68a63fc2e40f5359cd76c38ae9))
* use conditional error, otherwise the operator is stuck after etcd ([4b095bf](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/4b095bf5bacfdb87eace89340c2a9113f902a216))
* use correct namespace for dev ([#36](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/36)) ([7f5e3f4](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/7f5e3f45cc4ed4a6063cd0013dec5855f7b16980))

## [1.4.0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/v1.2.2...v1.4.0) (2025-11-25)

### Features

- add hash for kube-root-ca.crt configmap to konnectivity-agent ([#38](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/38)) ([9f4e977](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/9f4e9777ff3c4b55206d62e683414dccf0045c3c))
- add nix dev-shell ([#51](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/51)) ([d6c6eb1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/d6c6eb1eef3ee4f87a63a16875d865b211269546))
- auto-generate the metadata.yaml instead of manually adjusting it ([#42](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/42)) ([4a29346](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/4a2934667d62dca2fc1120396faf1e167246261c))
- update k8s dependencies + dependants ([#41](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/41)) ([885dcb5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/885dcb554d04fbb6c19f6186fe4cfee68022686e))

### Bug Fixes

- always create new titleCaser ([#37](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/37)) ([bafb5a1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/bafb5a138b2f9e0761550bf6fd8d3e2e945113e6))
- oidc jwks conformance ([#39](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/39)) ([5925f36](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/5925f361a4470d321743a1a85994da5ff147705b))
- use correct namespace for dev ([#36](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/36)) ([7f5e3f4](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/7f5e3f45cc4ed4a6063cd0013dec5855f7b16980))

## [1.2.2](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/v1.2.1...v1.2.2) (2025-10-24)

### Bug Fixes

- create a new ctx with cancel for each call ([#33](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/33)) ([2d6f7b8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2d6f7b860d0b80336769694016784f7af7d522d7))

## [1.2.1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/v1.2.0...v1.2.1) (2025-10-20)

### Bug Fixes

- **ci:** apparently hidden in changelog means ignored completely ([0d1733e](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/0d1733eb6901230026d7ed4d6629d4d661e08141))

## [1.2.0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/v1.1.1...v1.2.0) (2025-10-18)

### Features

- add whole lifecycle test ([#26](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/26)) ([2cb6170](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2cb617000197236f65314b68f413004beb6c9827))

### Bug Fixes

- don't set NODE_IP for kubeproxy, otherwise it fails without cloud provider ([#27](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/27)) ([609f3c7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/609f3c70f1f7702605ad9fec718a627b7af02437))

## [1.1.1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/v1.1.0...v1.1.1) (2025-10-01)

### Bug Fixes

- use conditional error, otherwise the operator is stuck after etcd ([4b095bf](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/4b095bf5bacfdb87eace89340c2a9113f902a216))

## [1.1.0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/v1.0.0...v1.1.0) (2025-09-30)

### Features

- add CiliumNetworkPolicies ([6a9e1b0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6a9e1b006fe5cab74da5fef00ebcb90d72534ad2))
- switch konnectivity agent from DaemonSet to Deployment ([6a9e1b0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6a9e1b006fe5cab74da5fef00ebcb90d72534ad2))

## 1.0.0 (2025-09-24)

### Features

- add auditConfiguration option ([e5e78d5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/e5e78d5a60dd94c5ff882ef7c3de70b004e73ae4))
- add authentication for audit webhooks ([#11](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/11)) ([0a64b9c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/0a64b9cd8bf477c32a55ae922144ae65e3da61f7))
- add ExternalManagedControlPlane field and enhance resource reconciliation ([b4b5094](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4b50941242c77dc98a14890e9307ed34f959f7a))
- add HostedControlPlaneTemplate CRD and Kubernetes deployment manifests ([12123f3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/12123f30b01c3631fd0603e4c32a6a78aa08534c))
- add more tests ([f18cd8a](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/f18cd8a82dc7145db10d9ba10c2eea4280b4c8bd))
- add network policy support with component-based access control ([6475139](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6475139725c04e06f81e2cb9d5a5c4083fec7811))
- add NetworkPolicies ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
- add OpenTelemetry tracing to reconciler checksum and deployment operations ([4652889](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/465288956d142da3536a6a8ae0fc6a06f395ffdc))
- add project information ([2622bc1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2622bc1922d6a7f839f3808b2439ff2d6503997a))
- add tests for util package ([27396c9](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27396c91084008671c223b438e37619ac8c5a6c4))
- allow overriding the images ([#5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/5)) ([cb9dd85](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/cb9dd850e681f4ce26a63fb132a869d37e7bfb43))
- allow referencing a secret from another namespace for audit webhook authentication ([#12](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/12)) ([aa563a2](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa563a21fbadffa5827591dd23a8d586435e646b))
- bootstrap Cluster API hosted control plane provider ([b4934f7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4934f7dca85e7640fb60cb17514f398dbfbaf47))
- compare with hardcoded `main` for default branch ([67a77d8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/67a77d8ec8f9db58d490721de9b01e00e4027045))
- **controller:** implement core HostedControlPlane service reconciliation ([09fa27c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/09fa27caa22b5c737dfbf6dd2766f5e99702237c))
- create etcd snapshot ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
- **deployment:** enhance control plane component deployment logic ([0601374](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/06013746a7fb03fef2c37b6a1b7d0b0a40adb850))
- **etcd:** implement ETCD cluster management and enhance certificates ([1b4b1cc](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/1b4b1ccb81a66b1976291139d315283ca4a5580f))
- expand certificate management and add resource checksumming ([7b1cfb8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/7b1cfb83e5e6a6348738715a2a6e10c87d96b307))
- extend API server configuration with mount support and workload cluster improvements ([a10410b](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a10410bff7e798d4b9f319806d5266cc8b3fedcc))
- extract workload config reconciliation into dedicated reconciler ([478a5f5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/478a5f551e2dbc775793d38f16ac1331ecbbd1a6))
- implement s3 backup ([d2dc916](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/d2dc9168d673a4a9e9b8ee75a7635334270e89b5))
- refactor workload reconciler to use dedicated RBAC and konnectivity reconcilers ([7458973](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74589732a47a64143ea9654b050dfc5181386e77))
- setup building and CI ([a3982ed](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a3982eda20b60fcfab7035e43d70e58a3b659263))
- update dependencies and use etcd client version for image ([aa7e3a7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa7e3a7a8d508477037966be2b9ca325518a099e))
- use helper function for getting IPs ([9bd07d3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/9bd07d308defdc246cfb965780b0ddf5f3f78562))
- Use mermaid instead of ASCII ([86e674e](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/86e674e19158f1554114d8b695e730671af9ce70))

### Bug Fixes

- **ci:** apparently there is no variable expansion here ([ebc0361](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/ebc0361ad7035684823128f9f59df2012e1b90a6))
- **ci:** missing release-please manifest ([284f6f4](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/284f6f47a4796629be50fcb5207ee4ddc3482193))
- code scanning alerts ([27d2866](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27d2866497cd7f6795e16f57762b2c4b4f9c6e64))
- implement resource deletion ([931b320](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/931b320e94f7ce8a66a01fed12cf003b963239f2))
- README formatting ([b3b3dea](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b3b3dea6de1f0d68a63fc2e40f5359cd76c38ae9))

## 1.0.0 (2025-09-24)

### Features

- add auditConfiguration option ([e5e78d5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/e5e78d5a60dd94c5ff882ef7c3de70b004e73ae4))
- add authentication for audit webhooks ([#11](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/11)) ([0a64b9c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/0a64b9cd8bf477c32a55ae922144ae65e3da61f7))
- add ExternalManagedControlPlane field and enhance resource reconciliation ([b4b5094](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4b50941242c77dc98a14890e9307ed34f959f7a))
- add HostedControlPlaneTemplate CRD and Kubernetes deployment manifests ([12123f3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/12123f30b01c3631fd0603e4c32a6a78aa08534c))
- add more tests ([f18cd8a](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/f18cd8a82dc7145db10d9ba10c2eea4280b4c8bd))
- add network policy support with component-based access control ([6475139](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6475139725c04e06f81e2cb9d5a5c4083fec7811))
- add NetworkPolicies ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
- add OpenTelemetry tracing to reconciler checksum and deployment operations ([4652889](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/465288956d142da3536a6a8ae0fc6a06f395ffdc))
- add project information ([2622bc1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2622bc1922d6a7f839f3808b2439ff2d6503997a))
- add tests for util package ([27396c9](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27396c91084008671c223b438e37619ac8c5a6c4))
- allow overriding the images ([#5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/5)) ([cb9dd85](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/cb9dd850e681f4ce26a63fb132a869d37e7bfb43))
- allow referencing a secret from another namespace for audit webhook authentication ([#12](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/12)) ([aa563a2](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa563a21fbadffa5827591dd23a8d586435e646b))
- bootstrap Cluster API hosted control plane provider ([b4934f7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4934f7dca85e7640fb60cb17514f398dbfbaf47))
- compare with hardcoded `main` for default branch ([67a77d8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/67a77d8ec8f9db58d490721de9b01e00e4027045))
- **controller:** implement core HostedControlPlane service reconciliation ([09fa27c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/09fa27caa22b5c737dfbf6dd2766f5e99702237c))
- create etcd snapshot ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
- **deployment:** enhance control plane component deployment logic ([0601374](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/06013746a7fb03fef2c37b6a1b7d0b0a40adb850))
- **etcd:** implement ETCD cluster management and enhance certificates ([1b4b1cc](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/1b4b1ccb81a66b1976291139d315283ca4a5580f))
- expand certificate management and add resource checksumming ([7b1cfb8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/7b1cfb83e5e6a6348738715a2a6e10c87d96b307))
- extend API server configuration with mount support and workload cluster improvements ([a10410b](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a10410bff7e798d4b9f319806d5266cc8b3fedcc))
- extract workload config reconciliation into dedicated reconciler ([478a5f5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/478a5f551e2dbc775793d38f16ac1331ecbbd1a6))
- implement s3 backup ([d2dc916](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/d2dc9168d673a4a9e9b8ee75a7635334270e89b5))
- refactor workload reconciler to use dedicated RBAC and konnectivity reconcilers ([7458973](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74589732a47a64143ea9654b050dfc5181386e77))
- setup building and CI ([a3982ed](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a3982eda20b60fcfab7035e43d70e58a3b659263))
- update dependencies and use etcd client version for image ([aa7e3a7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa7e3a7a8d508477037966be2b9ca325518a099e))
- use helper function for getting IPs ([9bd07d3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/9bd07d308defdc246cfb965780b0ddf5f3f78562))
- Use mermaid instead of ASCII ([86e674e](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/86e674e19158f1554114d8b695e730671af9ce70))

### Bug Fixes

- **ci:** apparently there is no variable expansion here ([ebc0361](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/ebc0361ad7035684823128f9f59df2012e1b90a6))
- **ci:** missing release-please manifest ([284f6f4](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/284f6f47a4796629be50fcb5207ee4ddc3482193))
- code scanning alerts ([27d2866](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27d2866497cd7f6795e16f57762b2c4b4f9c6e64))
- implement resource deletion ([931b320](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/931b320e94f7ce8a66a01fed12cf003b963239f2))
- README formatting ([b3b3dea](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b3b3dea6de1f0d68a63fc2e40f5359cd76c38ae9))

## [0.2.0](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/compare/cluster-api-control-plane-provider-hcp-v0.1.0...cluster-api-control-plane-provider-hcp-v0.2.0) (2025-09-23)

### Features

- add auditConfiguration option ([e5e78d5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/e5e78d5a60dd94c5ff882ef7c3de70b004e73ae4))
- add authentication for audit webhooks ([#11](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/11)) ([0a64b9c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/0a64b9cd8bf477c32a55ae922144ae65e3da61f7))
- add ExternalManagedControlPlane field and enhance resource reconciliation ([b4b5094](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4b50941242c77dc98a14890e9307ed34f959f7a))
- add HostedControlPlaneTemplate CRD and Kubernetes deployment manifests ([12123f3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/12123f30b01c3631fd0603e4c32a6a78aa08534c))
- add more tests ([f18cd8a](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/f18cd8a82dc7145db10d9ba10c2eea4280b4c8bd))
- add network policy support with component-based access control ([6475139](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/6475139725c04e06f81e2cb9d5a5c4083fec7811))
- add NetworkPolicies ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
- add OpenTelemetry tracing to reconciler checksum and deployment operations ([4652889](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/465288956d142da3536a6a8ae0fc6a06f395ffdc))
- add project information ([2622bc1](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/2622bc1922d6a7f839f3808b2439ff2d6503997a))
- add tests for util package ([27396c9](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27396c91084008671c223b438e37619ac8c5a6c4))
- allow overriding the images ([#5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/5)) ([cb9dd85](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/cb9dd850e681f4ce26a63fb132a869d37e7bfb43))
- allow referencing a secret from another namespace for audit webhook authentication ([#12](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/issues/12)) ([aa563a2](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa563a21fbadffa5827591dd23a8d586435e646b))
- bootstrap Cluster API hosted control plane provider ([b4934f7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b4934f7dca85e7640fb60cb17514f398dbfbaf47))
- compare with hardcoded `main` for default branch ([67a77d8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/67a77d8ec8f9db58d490721de9b01e00e4027045))
- **controller:** implement core HostedControlPlane service reconciliation ([09fa27c](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/09fa27caa22b5c737dfbf6dd2766f5e99702237c))
- create etcd snapshot ([74a96cb](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74a96cb25035b3c176b1b27c2379e78c03c51062))
- **deployment:** enhance control plane component deployment logic ([0601374](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/06013746a7fb03fef2c37b6a1b7d0b0a40adb850))
- **etcd:** implement ETCD cluster management and enhance certificates ([1b4b1cc](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/1b4b1ccb81a66b1976291139d315283ca4a5580f))
- expand certificate management and add resource checksumming ([7b1cfb8](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/7b1cfb83e5e6a6348738715a2a6e10c87d96b307))
- extend API server configuration with mount support and workload cluster improvements ([a10410b](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a10410bff7e798d4b9f319806d5266cc8b3fedcc))
- extract workload config reconciliation into dedicated reconciler ([478a5f5](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/478a5f551e2dbc775793d38f16ac1331ecbbd1a6))
- implement s3 backup ([d2dc916](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/d2dc9168d673a4a9e9b8ee75a7635334270e89b5))
- refactor workload reconciler to use dedicated RBAC and konnectivity reconcilers ([7458973](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/74589732a47a64143ea9654b050dfc5181386e77))
- setup building and CI ([a3982ed](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/a3982eda20b60fcfab7035e43d70e58a3b659263))
- update dependencies and use etcd client version for image ([aa7e3a7](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/aa7e3a7a8d508477037966be2b9ca325518a099e))
- use helper function for getting IPs ([9bd07d3](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/9bd07d308defdc246cfb965780b0ddf5f3f78562))
- Use mermaid instead of ASCII ([86e674e](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/86e674e19158f1554114d8b695e730671af9ce70))

### Bug Fixes

- **ci:** apparently there is no variable expansion here ([ebc0361](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/ebc0361ad7035684823128f9f59df2012e1b90a6))
- **ci:** missing release-please manifest ([284f6f4](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/284f6f47a4796629be50fcb5207ee4ddc3482193))
- code scanning alerts ([27d2866](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/27d2866497cd7f6795e16f57762b2c4b4f9c6e64))
- implement resource deletion ([931b320](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/931b320e94f7ce8a66a01fed12cf003b963239f2))
- README formatting ([b3b3dea](https://github.com/teutonet/cluster-api-provider-hosted-control-plane/commit/b3b3dea6de1f0d68a63fc2e40f5359cd76c38ae9))
