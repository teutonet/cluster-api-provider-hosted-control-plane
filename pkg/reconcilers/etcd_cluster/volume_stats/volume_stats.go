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

var errNoStatsForNode = errors.New("no stats available for node")

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

func (p *kubeletEtcdVolumeStatsProvider) GetMaxEtcdVolumeUsage(
	ctx context.Context,
	pods []corev1.Pod,
) (int64, error) {
	return tracing.WithSpan(ctx, tracer, "GetMaxEtcdVolumeUsage",
		func(ctx context.Context, span trace.Span) (int64, error) {
			nodeStats := parallel.Map(
				slices.Uniq(slices.Map(pods, func(pod corev1.Pod, _ int) string { return pod.Spec.NodeName })),
				func(node string, _ int) slices.Tuple2[string, slices.Tuple2[kubeletstatsv1alpha1.Summary, error]] {
					summary, err := p.getNodeStats(ctx, node)
					return slices.T2(node, slices.T2(summary, err))
				},
			)

			statsByNode := slices.SliceToMap(
				nodeStats,
				func(
					t slices.Tuple2[string, slices.Tuple2[kubeletstatsv1alpha1.Summary, error]],
				) (string, slices.Tuple2[kubeletstatsv1alpha1.Summary, error]) {
					return t.A, t.B
				},
			)

			results := slices.Map(pods, func(pod corev1.Pod, _ int) slices.Tuple2[int64, error] {
				nodeResult, ok := statsByNode[pod.Spec.NodeName]
				if !ok {
					return slices.T2(int64(0), fmt.Errorf("%w for node %s", errNoStatsForNode, pod.Spec.NodeName))
				}
				if nodeResult.B != nil {
					return slices.T2(int64(0), nodeResult.B)
				}
				usage := p.extractPodVolumeUsage(nodeResult.A, pod.Namespace, pod.Name)
				return slices.T2[int64, error](usage, nil)
			})

			sizes, errs := slices.Unzip2(results)
			maxUsage := slices.Max(sizes)

			span.SetAttributes(attribute.Int64("etcd.volume.filesystem_usage_bytes", maxUsage))
			return maxUsage, errors.Join(errs...)
		},
	)
}

//+kubebuilder:rbac:groups="",resources=nodes/proxy,verbs=get

func (p *kubeletEtcdVolumeStatsProvider) getNodeStats(
	ctx context.Context,
	nodeName string,
) (kubeletstatsv1alpha1.Summary, error) {
	return tracing.WithSpan(ctx, tracer, "getNodeStats",
		func(ctx context.Context, span trace.Span) (kubeletstatsv1alpha1.Summary, error) {
			span.SetAttributes(attribute.String("node", nodeName))

			data, err := p.client.CoreV1().RESTClient().
				Get().
				AbsPath("/api/v1/nodes", nodeName, "proxy", "stats", "summary").
				DoRaw(ctx)
			if err != nil {
				return kubeletstatsv1alpha1.Summary{}, fmt.Errorf(
					"failed to query kubelet stats summary for node %s: %w",
					nodeName,
					err,
				)
			}

			var summary kubeletstatsv1alpha1.Summary
			if err := json.Unmarshal(data, &summary); err != nil {
				return kubeletstatsv1alpha1.Summary{}, fmt.Errorf("failed to unmarshal stats summary: %w", err)
			}

			return summary, nil
		},
	)
}

func (p *kubeletEtcdVolumeStatsProvider) extractPodVolumeUsage(
	summary kubeletstatsv1alpha1.Summary,
	namespace string,
	podName string,
) int64 {
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
			return int64(min(*volStats.UsedBytes, uint64(math.MaxInt64)))
		}
	}

	return 0
}

func isEtcdDataVolume(volStats *kubeletstatsv1alpha1.VolumeStats) bool {
	if volStats.PVCRef != nil {
		return strings.HasPrefix(volStats.PVCRef.Name, "etcd-data-")
	}
	return volStats.Name == "etcd-data"
}
