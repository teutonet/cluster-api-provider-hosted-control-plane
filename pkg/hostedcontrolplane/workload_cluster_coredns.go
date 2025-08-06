package hostedcontrolplane

import (
	"github.com/coredns/corefile-migration/migration"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
)

type coreDNSMigrator interface {
	Migrate(currentVersion string, toVersion string, corefile string, deprecations bool) (string, error)
}

type CoreDNSMigrator struct{}

var _ coreDNSMigrator = &CoreDNSMigrator{}

func (c *CoreDNSMigrator) Migrate(
	fromCoreDNSVersion string,
	toCoreDNSVersion string,
	corefile string,
	deprecations bool,
) (string, error) {
	result, err := migration.Migrate(fromCoreDNSVersion, toCoreDNSVersion, corefile, deprecations)
	return result, errorsUtil.IfErrErrorf("failed to migrate CoreDNS configuration: %w", err)
}
