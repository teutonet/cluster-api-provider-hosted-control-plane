package util

import (
	"errors"
	"net/netip"
	"strings"
)

var ErrNoIPAddress = errors.New("no usable ip address")

// ResolveAddresses returns the ip addresses an external object (load balancer, gateway) is reachable on. Malformed
// ips are skipped. Some of them only report a nip.io hostname that embeds the address (`192.0.2.10.nip.io`); that is
// trimmed to the address, but only if there is no real ip address. If there is none at all, ErrNoIPAddress is returned.
func ResolveAddresses(ips []string, hostnames []string) ([]netip.Addr, error) {
	if addresses := parseAddresses(ips); len(addresses) > 0 {
		return addresses, nil
	}

	var nipIOAddresses []string
	for _, hostname := range hostnames {
		if trimmed, found := strings.CutSuffix(hostname, ".nip.io"); found {
			nipIOAddresses = append(nipIOAddresses, trimmed)
		}
	}
	if addresses := parseAddresses(nipIOAddresses); len(addresses) > 0 {
		return addresses, nil
	}

	return nil, ErrNoIPAddress
}

func parseAddresses(values []string) []netip.Addr {
	var addresses []netip.Addr
	for _, value := range values {
		if address, err := netip.ParseAddr(value); err == nil {
			addresses = append(addresses, address)
		}
	}
	return addresses
}
