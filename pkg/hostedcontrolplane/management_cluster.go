package hostedcontrolplane

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ManagementCluster interface {
	client.Reader

	GetWorkloadCluster(ctx context.Context, clusterKey client.ObjectKey) (WorkloadCluster, error)
}

type Management struct {
	Client       client.Reader
	ClusterCache clustercache.ClusterCache
}

var _ ManagementCluster = &Management{}

func (m *Management) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return m.Client.Get(ctx, key, obj, opts...)
}

func (m *Management) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return m.Client.List(ctx, list, opts...)
}

func (m *Management) GetWorkloadCluster(ctx context.Context, clusterKey client.ObjectKey) (WorkloadCluster, error) {
	restConfig, err := m.ClusterCache.GetRESTConfig(ctx, clusterKey)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", clusterKey.String(), err)
	}
	restConfig = rest.CopyConfig(restConfig)
	restConfig.Timeout = 30 * time.Second

	workloadClusterClient, err := m.ClusterCache.GetClient(ctx, clusterKey)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", clusterKey.String(), err)
	}

	return &Workload{
		restConfig:      restConfig,
		Client:          workloadClusterClient,
		CoreDNSMigrator: &CoreDNSMigrator{},
	}, nil
}
