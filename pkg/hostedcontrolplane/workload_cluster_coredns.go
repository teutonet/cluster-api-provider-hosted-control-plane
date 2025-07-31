package hostedcontrolplane

import "github.com/coredns/corefile-migration/migration"

type coreDNSMigrator interface {
	Migrate(currentVersion string, toVersion string, corefile string, deprecations bool) (string, error)
}

type CoreDNSMigrator struct{}

var _ coreDNSMigrator = &CoreDNSMigrator{}

func (c *CoreDNSMigrator) Migrate(
	fromCoreDNSVersion, toCoreDNSVersion, corefile string,
	deprecations bool,
) (string, error) {
	return migration.Migrate(fromCoreDNSVersion, toCoreDNSVersion, corefile, deprecations)
}
