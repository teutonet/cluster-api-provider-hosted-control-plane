package util

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 is used for checksums, not cryptographic security
	"encoding/hex"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func calculateChecksum(maps ...map[string]any) string {
	checksums := make([]string, 0, len(maps))
	for _, m := range maps {
		//nolint:gosec // MD5 is used for checksums, not cryptographic security
		hasher := md5.New()

		keys := make([]string, 0, len(m))
		for key := range m {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			hasher.Write([]byte(key))
			value := fmt.Sprintf("%v", m[key])
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

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get

func CalculateSecretChecksum(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	secretNames []string,
) (string, error) {
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
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get

func CalculateConfigMapChecksum(
	ctx context.Context,
	kubernetesClient kubernetes.Interface,
	namespace string,
	configMapNames []string,
) (string, error) {
	configMapMaps := make([]map[string]any, 0, len(configMapNames))
	for _, configMapName := range configMapNames {
		configMap, err := kubernetesClient.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
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
}
