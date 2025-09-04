package errors

import (
	"fmt"
)

// IfErrErrorf returns a wrapped error if err (the last arg) is not nil.
func IfErrErrorf(format string, args ...interface{}) error {
	if len(args) < 1 {
		return nil
	}
	err, isErr := args[len(args)-1].(error)
	if isErr && err != nil {
		//nolint:err113 // this is a wrapper function
		return fmt.Errorf(format, args...)
	}
	return nil
}
