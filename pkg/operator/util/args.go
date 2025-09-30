package util

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/recorder"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
)

type ArgOption struct {
	Prefix    *string
	Delimiter *string
}

var argumentOverriddenEvent = "ArgumentOverridden"

func argsToSlice(
	userArgs map[string]string,
	controllerArgs map[string]string,
	opts *ArgOption,
) []string {
	type arg struct {
		key   string
		value string
	}

	prefix := "--"
	joiner := "="
	if opts != nil {
		if opts.Prefix != nil {
			prefix = *opts.Prefix
		}
		if opts.Delimiter != nil {
			joiner = *opts.Delimiter
		}
	}
	argsSlice := slices.MapToSlice(slices.Assign(userArgs, controllerArgs), func(key string, value string) arg {
		return arg{
			key:   key,
			value: value,
		}
	})
	sort.Slice(argsSlice, func(i, j int) bool {
		return argsSlice[i].key < argsSlice[j].key
	})
	return slices.FlatMap(argsSlice, func(arg arg, _ int) []string {
		if joiner == " " {
			return []string{fmt.Sprintf("%s%s", prefix, arg.key), arg.value}
		}
		return []string{fmt.Sprintf("%s%s%s%s", prefix, arg.key, joiner, arg.value)}
	})
}

// ArgsToSlice merges user and controller arguments with observability for overrides.
// Controller args override user args and overrides are logged, traced, and emitted as events.
func ArgsToSlice(
	ctx context.Context,
	userArgs map[string]string,
	controllerArgs map[string]string,
	opts ...ArgOption,
) []string {
	if overriddenKeys := slices.Filter(slices.Keys(userArgs), func(key string, _ int) bool {
		return slices.HasKey(controllerArgs, key)
	}); len(overriddenKeys) > 0 {
		logger := logr.FromContextAsSlogLogger(ctx)
		span := trace.SpanFromContext(ctx)
		eventRecorder := recorder.FromContext(ctx)

		for _, key := range slices.Filter(overriddenKeys, func(key string, _ int) bool {
			return userArgs[key] != controllerArgs[key]
		}) {
			logger.WarnContext(
				ctx,
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
				corev1.EventTypeWarning, argumentOverriddenEvent,
				"User argument overridden by controller: %s (userValue=%s, controllerValue=%s)",
				key, userArgs[key], controllerArgs[key],
			)
		}
	}

	var opt ArgOption
	if len(opts) > 0 {
		opt = opts[0]
	}
	return argsToSlice(userArgs, controllerArgs, &opt)
}
