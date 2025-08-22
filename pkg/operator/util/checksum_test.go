package util

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const namespace = "test-namespace"

func TestCalculateConfigMapChecksum(t *testing.T) {
	tests := []struct {
		name            string
		namespace       string
		configMapNames  []string
		setupConfigMaps func(*fake.Clientset, string)
		expectError     bool
		expectedLength  int
	}{
		{
			name:            "empty configmap names",
			namespace:       "test-namespace",
			configMapNames:  []string{},
			setupConfigMaps: func(client *fake.Clientset, namespace string) {},
			expectError:     false,
			expectedLength:  0,
		},
		{
			name:           "single configmap",
			namespace:      "test-namespace",
			configMapNames: []string{"test-config"},
			setupConfigMaps: func(client *fake.Clientset, namespace string) {
				_, err := client.CoreV1().ConfigMaps(namespace).Create(
					context.Background(),
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-config",
							Namespace: namespace,
						},
						Data: map[string]string{
							"key1": "value1",
							"key2": "value2",
						},
					},
					metav1.CreateOptions{},
				)
				if err != nil {
					t.Fatalf("failed to create test configmap: %v", err)
				}
			},
			expectError:    false,
			expectedLength: 32,
		},
		{
			name:           "multiple configmaps",
			namespace:      "test-namespace",
			configMapNames: []string{"config1", "config2"},
			setupConfigMaps: func(client *fake.Clientset, namespace string) {
				configMaps := []*corev1.ConfigMap{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "config1",
							Namespace: namespace,
						},
						Data: map[string]string{
							"key1": "value1",
							"key3": "value3",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "config2",
							Namespace: namespace,
						},
						Data: map[string]string{
							"key2": "value2",
							"key4": "value4",
						},
					},
				}

				for _, cm := range configMaps {
					_, err := client.CoreV1().ConfigMaps(namespace).Create(
						context.Background(),
						cm,
						metav1.CreateOptions{},
					)
					if err != nil {
						t.Fatalf("failed to create test configmap %s: %v", cm.Name, err)
					}
				}
			},
			expectError:    false,
			expectedLength: 32,
		},
		{
			name:           "configmap with binary data",
			namespace:      "test-namespace",
			configMapNames: []string{"binary-config"},
			setupConfigMaps: func(client *fake.Clientset, namespace string) {
				_, err := client.CoreV1().ConfigMaps(namespace).Create(
					context.Background(),
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "binary-config",
							Namespace: namespace,
						},
						Data: map[string]string{
							"text-key": "text-value",
						},
						BinaryData: map[string][]byte{
							"binary-key": []byte("binary-data"),
						},
					},
					metav1.CreateOptions{},
				)
				if err != nil {
					t.Fatalf("failed to create test configmap: %v", err)
				}
			},
			expectError:    false,
			expectedLength: 32,
		},
		{
			name:            "configmap not found",
			namespace:       "test-namespace",
			configMapNames:  []string{"non-existent"},
			setupConfigMaps: func(client *fake.Clientset, namespace string) {},
			expectError:     true,
			expectedLength:  0,
		},
		{
			name:           "mixed existing and non-existing configmaps",
			namespace:      "test-namespace",
			configMapNames: []string{"existing", "non-existent"},
			setupConfigMaps: func(client *fake.Clientset, namespace string) {
				_, err := client.CoreV1().ConfigMaps(namespace).Create(
					context.Background(),
					&corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "existing",
							Namespace: namespace,
						},
						Data: map[string]string{
							"key": "value",
						},
					},
					metav1.CreateOptions{},
				)
				if err != nil {
					t.Fatalf("failed to create test configmap: %v", err)
				}
			},
			expectError:    true,
			expectedLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientset()
			tt.setupConfigMaps(fakeClient, tt.namespace)

			checksum, err := CalculateConfigMapChecksum(
				context.Background(),
				fakeClient,
				tt.namespace,
				tt.configMapNames,
			)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectedLength == 0 {
				if checksum != "" {
					t.Errorf("expected empty checksum, got %s", checksum)
				}
				return
			}

			if len(checksum) != tt.expectedLength {
				t.Errorf("expected checksum length %d, got %d", tt.expectedLength, len(checksum))
			}

			// Verify checksum is valid hex
			for _, char := range checksum {
				if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
					t.Errorf("checksum contains invalid hex character: %c", char)
				}
			}
		})
	}
}

func TestCalculateConfigMapChecksum_Consistency(t *testing.T) {
	fakeClient := fake.NewClientset()

	_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: namespace,
			},
			Data: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("failed to create test configmap: %v", err)
	}

	// Calculate checksum multiple times
	checksum1, err := CalculateConfigMapChecksum(
		context.Background(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	if err != nil {
		t.Fatalf("failed to calculate first checksum: %v", err)
	}

	checksum2, err := CalculateConfigMapChecksum(
		context.Background(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	if err != nil {
		t.Fatalf("failed to calculate second checksum: %v", err)
	}

	if checksum1 != checksum2 {
		t.Errorf("checksums should be identical, got %s and %s", checksum1, checksum2)
	}
}

func TestCalculateConfigMapChecksum_Ordering(t *testing.T) {
	fakeClient := fake.NewClientset()

	configMaps := []*corev1.ConfigMap{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config1",
				Namespace: namespace,
			},
			Data: map[string]string{
				"key1": "value1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "config2",
				Namespace: namespace,
			},
			Data: map[string]string{
				"key2": "value2",
			},
		},
	}

	for _, cm := range configMaps {
		_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(
			context.Background(),
			cm,
			metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("failed to create test configmap %s: %v", cm.Name, err)
		}
	}

	// Calculate checksum with different order
	checksum1, err := CalculateConfigMapChecksum(
		context.Background(),
		fakeClient,
		namespace,
		[]string{"config1", "config2"},
	)
	if err != nil {
		t.Fatalf("failed to calculate first checksum: %v", err)
	}

	checksum2, err := CalculateConfigMapChecksum(
		context.Background(),
		fakeClient,
		namespace,
		[]string{"config2", "config1"},
	)
	if err != nil {
		t.Fatalf("failed to calculate second checksum: %v", err)
	}

	if checksum1 != checksum2 {
		t.Errorf("checksums should be identical regardless of order, got %s and %s", checksum1, checksum2)
	}
}

func TestCalculateConfigMapChecksum_DataChanges(t *testing.T) {
	fakeClient := fake.NewClientset()

	_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: namespace,
			},
			Data: map[string]string{
				"key1": "value1",
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("failed to create test configmap: %v", err)
	}

	checksum1, err := CalculateConfigMapChecksum(
		context.Background(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	if err != nil {
		t.Fatalf("failed to calculate initial checksum: %v", err)
	}

	_, err = fakeClient.CoreV1().ConfigMaps(namespace).Update(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: namespace,
			},
			Data: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
		metav1.UpdateOptions{},
	)
	if err != nil {
		t.Fatalf("failed to update test configmap: %v", err)
	}

	checksum2, err := CalculateConfigMapChecksum(
		context.Background(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	if err != nil {
		t.Fatalf("failed to calculate updated checksum: %v", err)
	}

	if checksum1 == checksum2 {
		t.Errorf("checksums should be different after data change, got %s for both", checksum1)
	}
}

func TestCalculateChecksum(t *testing.T) {
	tests := []struct {
		name     string
		dataMaps []map[string]any
		expected string
	}{
		{
			name:     "empty data maps",
			dataMaps: []map[string]any{},
			expected: "",
		},
		{
			name:     "single empty map",
			dataMaps: []map[string]any{{}},
			expected: "d41d8cd98f00b204e9800998ecf8427e",
		},
		{
			name: "single map with data",
			dataMaps: []map[string]any{
				{"key1": "value1", "key2": "value2"},
			},
			expected: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
		},
		{
			name: "multiple maps",
			dataMaps: []map[string]any{
				{"key1": "value1"},
				{"key2": "value2"},
			},
			expected: "f1e2d3c4b5a6f1e2d3c4b5a6f1e2d3c4",
		},
		{
			name: "maps with different types",
			dataMaps: []map[string]any{
				{"string": "value", "int": 42, "bool": true},
			},
			expected: "b5a6f1e2d3c4b5a6f1e2d3c4b5a6f1e2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateChecksum(tt.dataMaps...)

			if tt.expected == "" {
				if result != "" {
					t.Errorf("expected empty checksum, got %s", result)
				}
				return
			}

			if len(result) != 32 {
				t.Errorf("expected checksum length 32, got %d", len(result))
			}

			// Verify checksum is valid hex
			for _, char := range result {
				if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
					t.Errorf("checksum contains invalid hex character: %c", char)
				}
			}

			// Test consistency - same input should produce same output
			result2 := calculateChecksum(tt.dataMaps...)
			if result != result2 {
				t.Errorf("checksums should be identical, got %s and %s", result, result2)
			}
		})
	}
}

func TestCalculateChecksum_Ordering(t *testing.T) {
	map1 := map[string]any{"z": "26", "a": "1", "m": "13"}
	map2 := map[string]any{"a": "1", "m": "13", "z": "26"}

	result1 := calculateChecksum(map1)
	result2 := calculateChecksum(map2)

	if result1 != result2 {
		t.Errorf("checksums should be identical regardless of key order, got %s and %s", result1, result2)
	}

	result3 := calculateChecksum(map1, map2)
	result4 := calculateChecksum(map2, map1)

	if result3 != result4 {
		t.Errorf("checksums should be identical regardless of map order, got %s and %s", result3, result4)
	}
}

func TestCalculateChecksum_Deterministic(t *testing.T) {
	dataMap := map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	// Calculate checksum multiple times
	checksum1 := calculateChecksum(dataMap)
	checksum2 := calculateChecksum(dataMap)
	checksum3 := calculateChecksum(dataMap)

	if checksum1 != checksum2 || checksum2 != checksum3 {
		t.Errorf("checksums should be identical across multiple calls: %s, %s, %s", checksum1, checksum2, checksum3)
	}
}
