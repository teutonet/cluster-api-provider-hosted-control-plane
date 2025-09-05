package operator

import (
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
)

func Test_fieldOwnerIsTheSame(t *testing.T) {
	t.Run("field owner is the same", func(t *testing.T) {
		if hostedControlPlaneControllerName != "hcp-controller" {
			t.Errorf("field owner has changed, this needs a migration, better undo it: got %s, want %s",
				hostedControlPlaneControllerName, "hcp-controller")
		}
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
		verify  func(resource *resource.Resource) error
		wantErr bool
	}{
		{
			name: "newResource doesn't fail",
			args: args{
				serviceName: "hcp-operator",
				version:     "dev",
			},
			verify: func(resource *resource.Resource) error {
				return nil
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newResource(tt.args.serviceName, tt.args.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("newResource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err := tt.verify(got); err != nil {
				t.Errorf("newResource() got = %v, error: %v", got, err)
			}
		})
	}
}
