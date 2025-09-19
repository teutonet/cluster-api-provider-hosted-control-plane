package errors

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"
)

func TestErrorfIfErr(t *testing.T) {
	g := NewWithT(t)
	type args struct {
		format string
		args   []any
	}
	tests := []struct {
		name        string
		args        args
		expectedErr error
	}{
		{
			name: "no args returns nil",
			args: args{
				format: "err: %w",
				args:   []any{},
			},
			expectedErr: nil,
		},
		{
			name: "nil error returns nil",
			args: args{
				format: "err: %w",
				args:   []any{nil},
			},
			expectedErr: nil,
		},
		{
			name: "error returns formatted error",
			args: args{
				format: "test: %w",
				args:   []any{errors.New("test")},
			},
			expectedErr: errors.New("test: test"),
		},
		{
			name: "complex error returns formatted error",
			args: args{
				format: "test (%s): %w",
				args:   []any{"field", errors.New("test")},
			},
			expectedErr: errors.New("test (field): test"),
		},
		{
			name: "complexer error returns formatted error",
			args: args{
				format: "test (%v): %w",
				args:   []any{struct{ name string }{"field"}, errors.New("test")},
			},
			expectedErr: errors.New("test ({field}): test"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := IfErrErrorf(tt.args.format, tt.args.args...)

			if tt.expectedErr == nil {
				g.Expect(err).To(BeNil())
			} else {
				// check the error message itself, as err is a wrapped error
				g.Expect(err).To(MatchError(tt.expectedErr.Error()))
			}
		})
	}
}
