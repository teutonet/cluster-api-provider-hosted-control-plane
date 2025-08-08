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
	return fmt.Errorf("failed to get object: %w", m.Client.Get(ctx, key, obj, opts...))
}

func (m *Management) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return fmt.Errorf("failed to list objects: %w", m.Client.List(ctx, list, opts...))
}

func (m *Management) GetWorkloadCluster(ctx context.Context, clusterKey client.ObjectKey) (WorkloadCluster, error) {
	restConfig, err := m.ClusterCache.GetRESTConfig(ctx, clusterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get REST config: %w", err)
	}
	restConfig = rest.CopyConfig(restConfig)
	restConfig.Timeout = 30 * time.Second

	workloadClusterClient, err := m.ClusterCache.GetClient(ctx, clusterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get workload cluster client: %w", err)
	}

	return &Workload{
		restConfig:      restConfig,
		Client:          workloadClusterClient,
		CoreDNSMigrator: &CoreDNSMigrator{},
	}, nil
}
