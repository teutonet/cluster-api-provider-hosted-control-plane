// Package etc contains the configuration for the operator.
package etc

import (
	"fmt"
	"log/slog"

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
}

func GetOperatorConfig() (Config, error) {
	var config Config

	if err := env.Parse(&config); err != nil {
		return Config{}, fmt.Errorf("failed to parse operator config: %w", err)
	}

	return config, nil
}
