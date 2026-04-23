package volume_stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	slices "github.com/samber/lo"
	"github.com/samber/lo/parallel"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	kubeletstatsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

var tracer = tracing.GetTracer("VolumeStats")

type EtcdVolumeStatsProvider interface {
	GetMaxEtcdVolumeUsage(
		ctx context.Context,
		pods []corev1.Pod,
	) (int64, error)
}

type kubeletEtcdVolumeStatsProvider struct {
	client *alias.ManagementClusterClient
}

var _ EtcdVolumeStatsProvider = &kubeletEtcdVolumeStatsProvider{}

func NewEtcdVolumeStatsProvider(client *alias.ManagementClusterClient) EtcdVolumeStatsProvider {
	return &kubeletEtcdVolumeStatsProvider{client: client}
}

//+kubebuilder:rbac:groups="",resources=nodes/proxy,verbs=get

func (p *kubeletEtcdVolumeStatsProvider) GetMaxEtcdVolumeUsage(
	ctx context.Context,
	pods []corev1.Pod,
) (int64, error) {
	return tracing.WithSpan(ctx, tracer, "GetMaxEtcdVolumeUsage",
		func(ctx context.Context, span trace.Span) (int64, error) {
			results := parallel.Map(pods, func(pod corev1.Pod, _ int) slices.Tuple2[int64, error] {
				usage, err := p.getEtcdVolumeUsageFromNode(ctx, pod.Spec.NodeName, pod.Namespace, pod.Name)
				return slices.T2(usage, err)
			})

			sizes, errs := slices.Unzip2(results)
			maxUsage := slices.Max(sizes)

			span.SetAttributes(attribute.Int64("etcd.volume.filesystem_usage_bytes", maxUsage))
			return maxUsage, errors.Join(errs...)
		},
	)
}

func (p *kubeletEtcdVolumeStatsProvider) getEtcdVolumeUsageFromNode(
	ctx context.Context,
	nodeName string,
	namespace string,
	podName string,
) (int64, error) {
	return tracing.WithSpan(ctx, tracer, "getEtcdVolumeUsageFromNode",
		func(ctx context.Context, span trace.Span) (int64, error) {
			span.SetAttributes(
				attribute.String("node", nodeName),
				attribute.String("pod", podName),
			)

			return p.fetchVolumeUsage(ctx, nodeName, namespace, podName)
		},
	)
}

func (p *kubeletEtcdVolumeStatsProvider) fetchVolumeUsage(
	ctx context.Context,
	nodeName string,
	namespace string,
	podName string,
) (int64, error) {
	data, err := p.client.CoreV1().RESTClient().
		Get().
		AbsPath("/api/v1/nodes", nodeName, "proxy", "stats", "summary").
		DoRaw(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query kubelet stats summary for node %s: %w", nodeName, err)
	}

	var summary kubeletstatsv1alpha1.Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		return 0, fmt.Errorf("failed to unmarshal stats summary: %w", err)
	}

	for i := range summary.Pods {
		podStats := &summary.Pods[i]
		if podStats.PodRef.Namespace != namespace || podStats.PodRef.Name != podName {
			continue
		}

		for j := range podStats.VolumeStats {
			volStats := &podStats.VolumeStats[j]
			if !isEtcdDataVolume(volStats) || volStats.UsedBytes == nil {
				continue
			}
			//nolint:gosec // capped at MaxInt64
			return int64(min(*volStats.UsedBytes, uint64(math.MaxInt64))), nil
		}
	}

	return 0, nil
}

func isEtcdDataVolume(volStats *kubeletstatsv1alpha1.VolumeStats) bool {
	if volStats.PVCRef != nil {
		return strings.HasPrefix(volStats.PVCRef.Name, "etcd-data-")
	}
	return volStats.Name == "etcd-data"
}
