package util

import (
	"fmt"
	"sort"

	slices "github.com/samber/lo"
)

func ArgsToSlice(args ...map[string]string) []string {
	argsSlice := slices.MapToSlice(slices.Assign(args...), func(key string, value string) string {
		return fmt.Sprintf("--%s=%s", key, value)
	})
	sort.Strings(argsSlice)
	return argsSlice
}
