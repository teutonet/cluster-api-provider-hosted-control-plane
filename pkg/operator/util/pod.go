package util

import (
	"errors"
	"fmt"
	"strings"

	slices "github.com/samber/lo"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

var ErrInvalidMount = errors.New("volume mount invalid or using non-existing volume")

func ValidateMounts(podSpec *corev1ac.PodSpecApplyConfiguration) error {
	volumeMounts := slices.FlatMap(podSpec.Containers,
		func(c corev1ac.ContainerApplyConfiguration, _ int) []corev1ac.VolumeMountApplyConfiguration {
			return c.VolumeMounts
		},
	)

	invalidMounts := slices.Filter(volumeMounts,
		func(mount corev1ac.VolumeMountApplyConfiguration, _ int) bool {
			return *mount.MountPath == "" ||
				slices.NoneBy(podSpec.Volumes, func(volume corev1ac.VolumeApplyConfiguration) bool {
					return *mount.Name == *volume.Name
				})
		},
	)

	if len(invalidMounts) > 0 {
		return fmt.Errorf(
			"VolumeMounts %s are using a non-existent volume or have an empty mountPath: %w",
			strings.Join(
				slices.Map(invalidMounts, func(mount corev1ac.VolumeMountApplyConfiguration, _ int) string {
					return *mount.Name
				}), ","),
			ErrInvalidMount,
		)
	}
	return nil
}
