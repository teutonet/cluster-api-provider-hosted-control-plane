package util_test

import (
	"net/netip"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestResolveAddresses(t *testing.T) {
	tests := []struct {
		name      string
		ips       []string
		hostnames []string
		expected  []netip.Addr
		wantErr   bool
	}{
		{
			name:     "ip",
			ips:      []string{"192.0.2.10"},
			expected: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name:     "ipv6",
			ips:      []string{"2001:db8::1"},
			expected: []netip.Addr{netip.MustParseAddr("2001:db8::1")},
		},
		{
			name:      "nip.io hostname is trimmed to its ip",
			hostnames: []string{"192.0.2.10.nip.io"},
			expected:  []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name:      "ip wins over nip.io hostname",
			ips:       []string{"192.0.2.10"},
			hostnames: []string{"192.0.2.11.nip.io"},
			expected:  []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name:     "malformed ips are skipped",
			ips:      []string{"999.1.1.1", "", "192.0.2.10"},
			expected: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		},
		{
			name:      "only malformed ips fall back to the nip.io hostname",
			ips:       []string{"999.1.1.1"},
			hostnames: []string{"192.0.2.11.nip.io"},
			expected:  []netip.Addr{netip.MustParseAddr("192.0.2.11")},
		},
		{
			name:      "other hostname is no ip",
			hostnames: []string{"gateway.example.com"},
			wantErr:   true,
		},
		{
			name:      "nip.io hostname without ip",
			hostnames: []string{"example.nip.io"},
			wantErr:   true,
		},
		{
			name:    "nothing",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)

			addresses, err := util.ResolveAddresses(tt.ips, tt.hostnames)

			if tt.wantErr {
				g.Expect(err).To(MatchError(util.ErrNoIPAddress))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(addresses).To(Equal(tt.expected))
		})
	}
}
