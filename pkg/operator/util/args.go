package util

import (
	"context"
	"fmt"
	"sort"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func ArgsToSlice(args ...map[string]string) []string {
	argsSlice := slices.MapToSlice(slices.Assign(args...), func(key string, value string) string {
		return fmt.Sprintf("--%s=%s", key, value)
	})
	sort.Strings(argsSlice)
	return argsSlice
}

// ArgsToSliceWithObservability merges user and controller arguments with observability for overrides.
// Controller args override user args (the same behavior as ArgsToSlice), but overrides are logged, traced, and
// emitted as events.
func ArgsToSliceWithObservability(
	ctx context.Context,
	userArgs map[string]string,
	controllerArgs map[string]string,
) []string {
	args := ArgsToSlice(userArgs, controllerArgs)

	if overriddenKeys := slices.Filter(slices.Keys(userArgs), func(key string, _ int) bool {
		return slices.HasKey(controllerArgs, key)
	}); len(overriddenKeys) > 0 {
		logger := log.FromContext(ctx)
		span := trace.SpanFromContext(ctx)
		eventRecorder := recorder.FromContext(ctx)

		for _, key := range slices.Filter(overriddenKeys, func(key string, _ int) bool {
			return userArgs[key] != controllerArgs[key]
		}) {
			logger.Info(
				"User argument overridden by controller",
				"arg", key,
				"userValue", userArgs[key],
				"controllerValue", controllerArgs[key],
			)

			span.AddEvent("Argument overridden", trace.WithAttributes(
				attribute.String("arg", key),
				attribute.String("userValue", userArgs[key]),
				attribute.String("controllerValue", controllerArgs[key]),
			))

			eventRecorder.Eventf(
				corev1.EventTypeWarning, "ArgumentOverridden",
				"User argument overridden by controller: %s (userValue=%s, controllerValue=%s)",
				key, userArgs[key], controllerArgs[key],
			)
		}
	}

	return args
}
