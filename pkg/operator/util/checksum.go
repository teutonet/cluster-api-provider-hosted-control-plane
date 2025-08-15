package util

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 is used for checksums, not cryptographic security
	"encoding/hex"
	"fmt"
	"sort"

	slices "github.com/samber/lo"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
)

func calculateChecksum(dataMaps ...map[string]any) string {
	checksums := make([]string, 0, len(dataMaps))
	for _, dataMap := range dataMaps {
		//nolint:gosec // MD5 is used for checksums, not cryptographic security
		hasher := md5.New()

		keys := make([]string, 0, len(dataMap))
		for key := range dataMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			hasher.Write([]byte(key))
			value := fmt.Sprintf("%v", dataMap[key])
			hasher.Write([]byte(value))
		}
		checksums = append(checksums, hex.EncodeToString(hasher.Sum(nil)))
	}

	if len(checksums) == 0 {
		return ""
	}

	sort.Strings(checksums)

	//nolint:gosec // MD5 is used for checksums, not cryptographic security
	finalHasher := md5.New()
	for _, checksum := range checksums {
		finalHasher.Write([]byte(checksum))
	}

	return hex.EncodeToString(finalHasher.Sum(nil))
}

func extractNames(
	volumes []corev1ac.VolumeApplyConfiguration,
	directAccess func(corev1ac.VolumeApplyConfiguration) string,
	projectedAccess func(configuration *corev1ac.VolumeProjectionApplyConfiguration) string,
) []string {
	return slices.Flatten(slices.Map(volumes, func(volume corev1ac.VolumeApplyConfiguration, _ int) []string {
		if value := directAccess(volume); value != "" {
			return []string{value}
		}
		if volume.Projected != nil && volume.Projected.Sources != nil {
			return slices.FilterMap(volume.Projected.Sources,
				func(source corev1ac.VolumeProjectionApplyConfiguration, _ int) (string, bool) {
					if value := projectedAccess(&source); value != "" {
						return value, true
					}
					return "", false
				},
			)
		}
		return nil
	}))
}

func extractSecretNames(volumes []corev1ac.VolumeApplyConfiguration) []string {
	return extractNames(volumes, func(volume corev1ac.VolumeApplyConfiguration) string {
		if volume.Secret != nil {
			return *volume.Secret.SecretName
		}
		return ""
	}, func(configuration *corev1ac.VolumeProjectionApplyConfiguration) string {
		if configuration.Secret != nil {
			return *configuration.Secret.Name
		}
		return ""
	})
}

func extractConfigMapNames(volumes []corev1ac.VolumeApplyConfiguration) []string {
	return extractNames(volumes, func(volume corev1ac.VolumeApplyConfiguration) string {
		if volume.ConfigMap != nil {
			return *volume.ConfigMap.Name
		}
		return ""
	}, func(configuration *corev1ac.VolumeProjectionApplyConfiguration) string {
		if configuration.ConfigMap != nil {
			return *configuration.ConfigMap.Name
		}
		return ""
	})
}

func SetChecksumAnnotations(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	template *corev1ac.PodTemplateSpecApplyConfiguration,
) (*corev1ac.PodTemplateSpecApplyConfiguration, error) {
	usedSecretName := extractSecretNames(template.Spec.Volumes)
	if len(usedSecretName) > 0 {
		if secretChecksum, err := CalculateSecretChecksum(ctx, client,
			namespace,
			usedSecretName,
		); err != nil {
			return nil, fmt.Errorf("failed to calculate secret checksum: %w", err)
		} else if secretChecksum != "" {
			template = template.WithAnnotations(map[string]string{
				"checksum/secrets": secretChecksum,
			})
		}
	}

	usedConfigMapNames := extractConfigMapNames(template.Spec.Volumes)
	if len(usedConfigMapNames) > 0 {
		if configMapChecksum, err := CalculateConfigMapChecksum(ctx, client,
			namespace,
			usedConfigMapNames,
		); err != nil {
			return nil, fmt.Errorf("failed to calculate configmap checksum: %w", err)
		} else if configMapChecksum != "" {
			template = template.WithAnnotations(map[string]string{
				"checksum/configmaps": configMapChecksum,
			})
		}
	}

	return template, nil
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func CalculateSecretChecksum(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	secretNames []string,
) (string, error) {
	return tracing.WithSpan(ctx, "checksum", "CalculateSecretChecksum",
		func(ctx context.Context, span trace.Span) (string, error) {
			secretMaps := make([]map[string]any, 0, len(secretNames))
			for _, secretName := range secretNames {
				secret, err := kubernetesClient.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
				if err != nil {
					return "", fmt.Errorf("secret fetch failed: %w", err)
				}

				secretMap := make(map[string]any)
				for key, value := range secret.Data {
					secretMap[key] = string(value)
				}
				secretMaps = append(secretMaps, secretMap)
			}

			return calculateChecksum(secretMaps...), nil
		},
	)
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get

func CalculateConfigMapChecksum(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	configMapNames []string,
) (string, error) {
	return tracing.WithSpan(ctx, "checksum", "CalculateConfigMapChecksum",
		func(ctx context.Context, span trace.Span) (string, error) {
			configMapMaps := make([]map[string]any, 0, len(configMapNames))
			for _, configMapName := range configMapNames {
				configMap, err := kubernetesClient.CoreV1().
					ConfigMaps(namespace).
					Get(ctx, configMapName, metav1.GetOptions{})
				if err != nil {
					return "", fmt.Errorf("configmap fetch failed: %w", err)
				}

				configMapMap := make(map[string]any)
				for key, value := range configMap.Data {
					configMapMap[key] = value
				}
				for key, value := range configMap.BinaryData {
					configMapMap[key] = string(value)
				}
				configMapMaps = append(configMapMaps, configMapMap)
			}

			return calculateChecksum(configMapMaps...), nil
		},
	)
}
