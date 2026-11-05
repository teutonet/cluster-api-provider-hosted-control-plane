package util

import (
	"context"
	"fmt"
	stdslices "slices"
	"strings"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/emit"
)

type ArgOption struct {
	Prefix    *string
	Delimiter *string
	// Repeated holds flags that are rendered once per value, in the given order. A key in here replaces the same key
	// in the user and controller args, an empty slice drops it.
	Repeated map[string][]string
}

var argumentOverriddenEvent = "ArgumentOverridden"

func argsToSlice(
	userArgs map[string]v1alpha1.ArgValue,
	controllerArgs map[string]string,
	opts *ArgOption,
) []string {
	type arg struct {
		key   string
		value string
	}

	prefix := "--"
	joiner := "="
	var repeated map[string][]string
	if opts != nil {
		if opts.Prefix != nil {
			prefix = *opts.Prefix
		}
		if opts.Delimiter != nil {
			joiner = *opts.Delimiter
		}
		repeated = opts.Repeated
	}

	merged := make(map[string][]string, len(userArgs)+len(controllerArgs)+len(repeated))
	for key, values := range userArgs {
		merged[key] = values
	}
	for key, value := range controllerArgs {
		merged[key] = []string{value}
	}
	for key, values := range repeated {
		merged[key] = values
	}

	keys := slices.Keys(merged)
	stdslices.Sort(keys)
	args := slices.FlatMap(keys, func(key string, _ int) []arg {
		return slices.Map(merged[key], func(value string, _ int) arg {
			return arg{key: key, value: value}
		})
	})
	return slices.FlatMap(args, func(arg arg, _ int) []string {
		if joiner == " " {
			return []string{fmt.Sprintf("%s%s", prefix, arg.key), arg.value}
		}
		return []string{fmt.Sprintf("%s%s%s%s", prefix, arg.key, joiner, arg.value)}
	})
}

// ArgsToSlice merges user and controller arguments with observability for overrides.
// Controller args override user args and overrides are logged, traced, and emitted as events.
// A user arg with several values is rendered once per value, in order.
func ArgsToSlice(
	ctx context.Context,
	userArgs map[string]v1alpha1.ArgValue,
	controllerArgs map[string]string,
	opts ...ArgOption,
) []string {
	userKeys := slices.Keys(userArgs)
	stdslices.Sort(userKeys)
	for _, key := range userKeys {
		controllerValue, overridden := controllerArgs[key]
		if overridden && !stdslices.Equal(userArgs[key], []string{controllerValue}) {
			emit.Warn(ctx, emit.SinkAll, nil,
				"ControllerArgumentTakesPrecedence",
				argumentOverriddenEvent,
				"User argument overridden by controller",
				"arg", key, "userValue", strings.Join(userArgs[key], ","), "controllerValue", controllerValue,
			)
		}
	}

	var opt ArgOption
	if len(opts) > 0 {
		opt = opts[0]
	}
	return argsToSlice(userArgs, controllerArgs, &opt)
}
