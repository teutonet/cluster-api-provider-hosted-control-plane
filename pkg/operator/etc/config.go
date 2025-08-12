// Package etc contains the configuration for the operator.
package etc

import (
	"fmt"

	env "github.com/caarlos0/env/v6"
)

type LogFormat string

var (
	JSON LogFormat = "json"
	TEXT LogFormat = "text"
)

type Config struct {
	LogDevMode              bool      `env:"LOG_DEV_MODE"              envDefault:"false"`
	LeaderElection          bool      `env:"LEADER_ELECTION"           envDefault:"true"`
	WebhookCertDir          string    `env:"WEBHOOK_CERT_DIR"`
	ServiceName             string    `env:"SERVICE_NAME"              envDefault:"hcp-controller"`
	MaxConcurrentReconciles int       `env:"MAX_CONCURRENT_RECONCILES" envDefault:"10"`
	LogFormat               LogFormat `env:"LOG_FORMAT"                envDefault:"json"`
}

func GetOperatorConfig() (Config, error) {
	var config Config

	if err := env.Parse(&config); err != nil {
		return Config{}, fmt.Errorf("failed to parse operator config: %w", err)
	}

	return config, nil
}
