package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CopyLabels(source metav1.ObjectMeta, target *metav1.ObjectMeta) {
	mergedLabels := target.DeepCopy().GetLabels()
	if mergedLabels == nil {
		mergedLabels = map[string]string{}
	}

	if source.GetLabels() != nil {
		for key, value := range source.GetLabels() {
			mergedLabels[key] = value
		}
	}

	target.SetLabels(mergedLabels)
}
