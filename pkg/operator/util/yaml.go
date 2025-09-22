package util

import (
	"bytes"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
	"k8s.io/apiserver/pkg/audit"
)

var (
	scheme  = runtime.NewScheme()
	encoder = json.NewSerializerWithOptions(json.SimpleMetaFactory{}, scheme, scheme, json.SerializerOptions{
		Yaml:   true,
		Pretty: true,
		Strict: false,
	})
	auditSerializer = serializer.NewCodecFactory(audit.Scheme, serializer.EnableStrict).
			EncoderForVersion(encoder, auditv1.SchemeGroupVersion)
)

func toYaml(obj runtime.Object, encoder runtime.Encoder) (string, error) {
	buf := bytes.NewBuffer([]byte{})
	if err := encoder.Encode(obj, buf); err != nil {
		return "", fmt.Errorf("failed to encode object to yaml: %w", err)
	}
	return buf.String(), nil
}

func ToYaml(obj runtime.Object) (string, error) {
	return toYaml(obj, encoder)
}

func PolicyToYaml(obj auditv1.Policy) (string, error) {
	return toYaml(&obj, auditSerializer)
}
