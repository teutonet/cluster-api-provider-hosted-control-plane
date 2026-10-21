# Contributing to Cluster API Control Plane Provider for Hosted Control Planes

Thank you for your interest in contributing to the Cluster API Control Plane Provider for Hosted Control Planes! 🎉

We welcome contributions of all kinds - whether you're fixing bugs, adding features, improving documentation, or helping
with testing and feedback. This guide will help you get started with the development process.

## 📋 Table of Contents

- [🤝 Code of Conduct](#-code-of-conduct)
- [🚀 Getting Started](#-getting-started)
- [🛠️ Development Environment](#️-development-environment)
- [🔄 Development Workflow](#-development-workflow)
- [📏 Code Standards](#-code-standards)
- [🧪 Testing Guidelines](#-testing-guidelines)
- [📬 Pull Request Process](#-pull-request-process)
- [🐛 Issue Guidelines](#-issue-guidelines)
- [🏗️ Architecture Overview](#️-architecture-overview)
- [🚢 Release Process](#-release-process)
- [❓ Getting Help](#-getting-help)
- [📄 License and Upstreaming](#-license-and-upstreaming)
- [🙏 Recognition](#-recognition)

## 🤝 Code of Conduct

This project adheres to a code of conduct that fosters an inclusive and welcoming environment for all contributors. By 
participating, you are expected to uphold these standards:

- **Be Respectful**: Treat all contributors with respect and kindness
- **Be Collaborative**: Work together constructively and give credit where due
- **Be Patient**: Help newcomers learn and grow
- **Be Professional**: Focus on technical discussions and avoid personal attacks

## 🚀 Getting Started

### Prerequisites

Before you begin, ensure you have the following tools installed:

- **Go 1.24+**: Programming language for the project
- **CRI**: For container image building and testing (podman, docker, etc.)
- **kubectl**: Kubernetes CLI tool
- **[Task](https://taskfile.dev)**: Build automation tool
- **Git**: Version control

### Optional but Recommended

- **[Telepresence](https://www.telepresence.io/)**: For local development against remote clusters
- **[GitHub CLI (gh)](https://cli.github.com/)**: For streamlined PR creation
- **[Cluster API](https://cluster-api.sigs.k8s.io/)**: For integration testing

## 🛠️ Development Environment

### 1. Fork and Clone the Repository

```bash
# Fork the repository on GitHub, then clone your fork
git clone https://github.com/YOUR_USERNAME/cluster-api-provider-hosted-control-plane.git
cd cluster-api-provider-hosted-control-plane

# Add upstream remote
git remote add upstream https://github.com/teutonet/cluster-api-provider-hosted-control-plane.git
```

### 2. Install Dependencies

```bash
# Verify Go installation
go version  # Should show 1.24+

# Install Task (if not already installed)
# On Archlinux: sudo pacman -S go-task
#    recommended to create an alias `alias task=go-task`
# The rest can check https://taskfile.dev/docs/installation

# Verify Task installation
task --version
```

## 🔄 Development Workflow

### Daily Development Commands

The project uses [Task](https://taskfile.dev) for build automation. Here are the most common commands:

```bash
# Build the project
task build

# Run tests with coverage
task test

# Run linting (without fixes)
task lint

# Run linting with automatic fixes
task lint fix=true

# Format code
task format

# Generate manifests and code
task generate
task manifests

# Run full CI pipeline (lint + test)
task ci

# Clean build artifacts
task clean
```

### Local Development Setup

#### For IDE-based Development

```bash
# Generate manifests and apply to cluster
task manifests
kubectl apply -f ./build/control-plane-components.yaml

# Start development mode (with telepresence if available)
task dev

# Now run the controller in your IDE using the provided .run configurations
```

#### For Command-line Development

```bash
# Run controller locally
go run ./cmd/hosted-control-plane-controller/main.go \
  --webhook-port=9443 \
  --enable-leader-election=false
```

### Making Changes

1. **Create a Feature Branch**
   ```bash
   git switch -c feature/your-feature-name
   ```

2. **Make Your Changes**
   - Write clear, focused commits
   - Follow the project's code standards
   - Add tests for new functionality
   - Update documentation as needed

3. **Test Your Changes**
   ```bash
   # Run the full CI pipeline
   task ci
   
   # Check for uncommitted generated files
   task check-diff
   ```

4. **Commit and Push**
   ```bash
   git add .
   git commit -m "feat: add awesome new feature"
   git push origin feature/your-feature-name
   ```

### Conventional Commits

This project follows [Conventional Commits](https://www.conventionalcommits.org/) for commit messages.
Please use the standard format:
`type: description` (e.g., `feat: add backup scheduling`, `fix: resolve controller memory leak`).

## 📏 Code Standards

### Go Code Style

The project enforces strict code standards using golangci-lint with over 70 enabled linters:

#### Formatting Rules
- **Line Length**: Maximum 120 characters (enforced by golines)
- **Formatting**: Use gofumpt (stricter than gofmt)
- **Imports**: Must follow specific alias rules (see below)

#### Import Alias Requirements

The project requires specific import aliases. Some key ones:

```go
// Kubernetes Core
import (
    corev1 "k8s.io/api/core/v1"
    appsv1 "k8s.io/api/apps/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    apierrors "k8s.io/apimachinery/pkg/api/errors"
    kerrors "k8s.io/apimachinery/pkg/util/errors"
)

// Controller Runtime
import (
    ctrl "sigs.k8s.io/controller-runtime"
)

// Cluster API
import (
    capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// Gateway API
import (
    gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Cert Manager
import (
    certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
)

// Project-specific aliases
import (
    errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
    slices "github.com/samber/lo"
)
```

#### Key Linting Rules
- **Error Handling**: All errors must be handled (`errcheck`)
- **Security**: Security analysis with `gosec`
- **Performance**: Performance-oriented checks (`perfsprint`, `prealloc`)
- **Correctness**: Logic and correctness checks (`govet`, `staticcheck`)
- **Style**: Consistent style enforcement (`revive`, `gocritic`)

### Generated Code

Some code is auto-generated. Never edit these files directly:

- `api/**/zz_generated.deepcopy.go` - Generated by controller-gen
- `api/**/zz_generated.conversion.go` - Generated by conversion-gen
- `build/` directory contents - Generated manifests

### Documentation Standards

- **API Documentation**: Keep API type documentation up to date

## 🧪 Testing Guidelines

### Test Organization

- **Unit Tests**: `*_test.go` files alongside source code
- **Coverage Target**: Aim for reasonable test coverage (run `task test` to see current coverage)

### Running Tests

```bash
# Run all tests with coverage
task test

# Run tests for specific package
go test ./pkg/reconcilers/...

# Run tests with verbose output
go test -v ./...
```

## 📬 Pull Request Process

### Before Submitting

1. **Run Full CI Pipeline**
   ```bash
   task ci
   ```

2. **Verify No Generated Code Changes**
   ```bash
   task check-diff
   ```

3. **Update Documentation if needed**

### PR Guidelines

- **Clear Title**: Use conventional commit format (`feat:`, `fix:`, `docs:`, etc.)
- **Focused Changes**: Keep PRs focused on a single feature or fix
- **Test Coverage**: Include tests for new functionality
- **Documentation**: Update docs for user-facing changes
- **Backward Compatibility**: Avoid breaking changes when possible

## 🐛 Issue Guidelines

### Bug Reports

When reporting bugs, please include:

- **Environment**: Kubernetes version, OS, etc.
- **Steps to Reproduce**: Clear, step-by-step instructions
- **Expected Behavior**: What should happen
- **Actual Behavior**: What actually happens
- **Logs**: Relevant log output or error messages
- **Additional Context**: Screenshots, configuration files, etc.

### Feature Requests

For feature requests, please provide:

- **Use Case**: Why this feature would be valuable
- **Proposed Solution**: How you envision it working
- **Alternatives**: Other solutions you've considered
- **Additional Context**: Examples, mockups, etc.

## 🏗️ Architecture Overview

### Project Structure

```
├── api/                    # API type definitions
│   └── v1alpha1/          # API version v1alpha1
├── cmd/                   # Main applications
├── config/                # Kubernetes manifests
├── pkg/                   # Library code
│   ├── hostedcontrolplane/ # Main controller
│   ├── operator/          # Operator utilities
│   ├── reconcilers/       # Specialized reconcilers
│   └── util/             # Shared utilities
└── build/                # Generated artifacts
```

### Key Components

1. **HostedControlPlane Controller**: Main reconciliation logic
2. **Reconcilers**: Specialized controllers for different aspects:
   - `etcd_cluster`: ETCD management with backup/restore
   - `apiserverresources`: API server deployment and services  
   - `certificates`: Certificate management via cert-manager
   - `infrastructure_cluster`: Infrastructure cluster setup
   - `workload`: Workload cluster components (RBAC, CoreDNS, kube-proxy)
   - `kubeconfig`: Kubeconfig generation
   - `tlsroutes`: Gateway API TLS route configuration

### Development Patterns

- **Reconciler Pattern**: Use controller-runtime reconciler pattern
- **Error Handling**: Use the project's error utilities (`errorsUtil`)
- **Tracing**: OpenTelemetry integration for observability
- **Conditions**: Status reporting using CAPI conditions

### Adding New Features

1. **API Changes**: Update types in `api/*/`
2. **Controller Logic**: Add reconciliation logic
3. **Tests**: Add comprehensive test coverage
4. **Documentation**: Update relevant documentation, if necessary

## 🚢 Release Process

Releases are handled by maintainers, but contributors should be aware of:

- **Semantic Versioning**: We follow semver (major.minor.patch)
- **Release Notes**: Breaking changes and their migration are documented
- **Backward Compatibility**: Breaking changes are rare and well-documented

## ❓ Getting Help

- **GitHub Issues**: For bug reports and feature requests
- **GitHub Discussions**: For questions and community discussion  
- **Code Review**: Maintainers will provide feedback on PRs

## 📄 License and Upstreaming

This project is licensed under AGPL-3.0-or-later with an additional permission for software that only uses its
Kubernetes resources and API types, see [LICENSE-EXCEPTION](LICENSE-EXCEPTION). By contributing you agree that your
contribution is licensed under the same terms.

If you modify this project, the license requires you to keep your modified version open source. We ask you to go one
step further and upstream your changes as a pull request instead of maintaining a fork. Everybody, including you,
benefits from a single maintained version.

## 🙏 Recognition

We value all contributions and recognize our contributors:

- **Contributors**: Listed in release notes
- **Maintainers**: Recognized in project documentation
- **Community**: Thanks to everyone who helps improve the project

---

Happy contributing! 🚀
