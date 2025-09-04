// Package util implements utilities.
package controller

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCopyLabels(t *testing.T) {
	type test struct {
		source metav1.ObjectMeta
		target *metav1.ObjectMeta
		want   map[string]string
	}

	tests := []test{
		{
			source: metav1.ObjectMeta{
				Labels: map[string]string{
					"foo":      "bar",
					"identity": "1234-testcluster-0199",
				},
			},
			target: &metav1.ObjectMeta{
				Labels: map[string]string{
					"cluster.x-k8s.io/example-label": "",
				},
			},
			want: map[string]string{
				"foo":                            "bar",
				"identity":                       "1234-testcluster-0199",
				"cluster.x-k8s.io/example-label": "",
			},
		},
		{
			source: metav1.ObjectMeta{
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			target: &metav1.ObjectMeta{
				Labels: map[string]string{
					"foo": "baz",
				},
			},
			want: map[string]string{
				"foo": "bar",
			},
		},
		{
			source: metav1.ObjectMeta{
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			target: &metav1.ObjectMeta{},
			want: map[string]string{
				"foo": "bar",
			},
		},
		{
			source: metav1.ObjectMeta{},
			target: &metav1.ObjectMeta{
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			want: map[string]string{
				"foo": "bar",
			},
		},
	}

	for _, tc := range tests {
		CopyLabels(tc.source, tc.target)
		got := tc.target.GetLabels()
		if !reflect.DeepEqual(tc.want, got) {
			t.Fatalf("expected: %v, got: %v", tc.want, got)
		}
	}
}
