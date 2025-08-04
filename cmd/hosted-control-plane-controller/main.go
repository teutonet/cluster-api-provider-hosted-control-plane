package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator"
	"github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/operator/etc"
	errorsUtil "github.com/teutonet/cluster-api-control-plane-provider-hcp/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		//nolint:forbidigo // we don't have a Context here
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error(
			"unable to run hosted control plane controller",
			"err",
			err,
		)
		os.Exit(1)
	}
}

func run() error {
	operatorConfig, err := etc.GetOperatorConfig()
	if err != nil {
		return fmt.Errorf("failed to get operator config: %w", err)
	}

	return errorsUtil.IfErrErrorf(
		"failed to start operator: %w",
		operator.Start(ctrl.SetupSignalHandler(), version, operatorConfig),
	)
}
