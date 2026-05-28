package operator

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func Test_fieldOwnerIsTheSame(t *testing.T) {
	t.Run("field owner is the same", func(t *testing.T) {
		g, _, _ := G(t)
		g.Expect(hostedControlPlaneControllerName).To(
			Equal("hcp-controller"),
			"field owner has changed, this needs a migration, better undo it: got %s, want %s",
			hostedControlPlaneControllerName, "hcp-controller",
		)
	})
}

func Test_newResource(t *testing.T) {
	type args struct {
		serviceName string
		version     string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "newResource doesn't fail",
			args: args{
				serviceName: "hcp-operator",
				version:     "dev",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _, _ := G(t)
			_, err := newResource(tt.args.serviceName, tt.args.version)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
