package util

import (
	"bytes"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
)

func ToYaml(obj runtime.Object) (*bytes.Buffer, error) {
	scheme := runtime.NewScheme()
	encoder := json.NewSerializerWithOptions(json.SimpleMetaFactory{}, scheme, scheme, json.SerializerOptions{
		Yaml:   true,
		Pretty: true,
		Strict: false,
	})

	buf := bytes.NewBuffer([]byte{})
	if err := encoder.Encode(obj, buf); err != nil {
		return nil, fmt.Errorf("failed to encode object to yaml: %w", err)
	}
	return buf, nil
}
