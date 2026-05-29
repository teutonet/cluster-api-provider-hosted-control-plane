package util

import (
	"cmp"
	"context"
	"fmt"
	stdslices "slices"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/emit"
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
	stdslices.SortFunc(argsSlice, func(left, right arg) int {
		return cmp.Compare(left.key, right.key)
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
		for _, key := range slices.Filter(overriddenKeys, func(key string, _ int) bool {
			return userArgs[key] != controllerArgs[key]
		}) {
			emit.Warn(ctx, emit.SinkAll, nil,
				"ControllerArgumentTakesPrecedence",
				argumentOverriddenEvent,
				"User argument overridden by controller",
				"arg", key, "userValue", userArgs[key], "controllerValue", controllerArgs[key],
			)
		}
	}

	var opt ArgOption
	if len(opts) > 0 {
		opt = opts[0]
	}
	return argsToSlice(userArgs, controllerArgs, &opt)
}
