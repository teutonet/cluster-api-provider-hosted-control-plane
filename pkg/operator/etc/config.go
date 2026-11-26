// Package etc contains the configuration for the operator.
package etc

import (
	"fmt"
	"log/slog"

	env "github.com/caarlos0/env/v11"
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

	// ManagementClusterOIDC controls whether the hosted API servers trust service account tokens issued by the
	// management cluster. This requires the management cluster to serve its OIDC discovery document and JWKS to
	// unauthenticated clients.
	ManagementClusterOIDC bool `env:"MANAGEMENT_CLUSTER_OIDC" envDefault:"true"`
}

func GetOperatorConfig() (Config, error) {
	var config Config

	if err := env.Parse(&config); err != nil {
		return Config{}, fmt.Errorf("failed to parse operator config: %w", err)
	}

	return config, nil
}
