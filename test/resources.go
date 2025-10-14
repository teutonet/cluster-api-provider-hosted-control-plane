package test

import (
	. "github.com/onsi/gomega"
	types2 "github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/resource"
)

func EqualResource(expected resource.Quantity) types2.GomegaMatcher {
	return WithTransform(
		func(r resource.Quantity) int64 { return r.Value() },
		And([]types2.GomegaMatcher{Equal(expected.Value())}...),
	)
}
