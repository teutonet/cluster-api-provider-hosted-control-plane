// Package etc contains the configuration for the operator.
package etc

import (
	"fmt"
	"log/slog"
	"time"

	env "github.com/caarlos0/env/v6"
)

type (
	LogFormat string
	LogLevel  string
)

var (
	JSON LogFormat = "json"
	TEXT LogFormat = "text"
)

type Config struct {
	LeaderElection          bool       `env:"LEADER_ELECTION"               envDefault:"true"`
	WebhookCertDir          string     `env:"WEBHOOK_CERT_DIR"`
	MaxConcurrentReconciles int        `env:"MAX_CONCURRENT_RECONCILES"     envDefault:"10"`
	ControllerNamespace     string     `env:"CONTROLLER_NAMESPACE,required"`
	LogFormat               LogFormat  `env:"LOG_FORMAT"                    envDefault:"json"`
	LogLevel                slog.Level `env:"LOG_LEVEL"                     envDefault:"INFO"`
	// ReconcileFilter, when non-empty, limits reconciliation to HostedControlPlanes whose name or
	// owner Cluster name matches. Supports bare name or "namespace/name" format. Debugging aid only.
	ReconcileFilter string `env:"HCP_RECONCILE_FILTER"`

	// CACertificateDuration overrides the duration of CA certificates
	// (cluster CA, etcd CA, front-proxy CA) created via cert-manager.
	// Default 48h matches the original behavior. Industry-standard
	// alternative: 87600h (10 years), matching kubeadm/RKE2/EKS defaults.
	CACertificateDuration time.Duration `env:"CA_CERTIFICATE_DURATION" envDefault:"48h"`
	// CertificateDuration overrides the duration of leaf certificates
	// (apiserver, etcd peer/server, controller-manager kubeconfig, etc.).
	// Default 24h matches the original behavior. Industry-standard
	// alternative: 8760h (1 year), matching kubeadm/RKE2/EKS defaults.
	CertificateDuration time.Duration `env:"CERTIFICATE_DURATION" envDefault:"24h"`
}

func GetOperatorConfig() (Config, error) {
	var config Config

	if err := env.Parse(&config); err != nil {
		return Config{}, fmt.Errorf("failed to parse operator config: %w", err)
	}

	return config, nil
}
