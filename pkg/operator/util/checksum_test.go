package util

import (
	"testing"

	. "github.com/onsi/gomega"
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
		setupConfigMaps func(*fake.Clientset, string, Gomega)
		expectError     bool
		expectedLength  int
	}{
		{
			name:            "empty configmap names",
			namespace:       "test-namespace",
			configMapNames:  []string{},
			setupConfigMaps: func(_ *fake.Clientset, _ string, _ Gomega) {},
			expectError:     false,
			expectedLength:  0,
		},
		{
			name:           "single configmap",
			namespace:      "test-namespace",
			configMapNames: []string{"test-config"},
			setupConfigMaps: func(client *fake.Clientset, namespace string, g Gomega) {
				_, err := client.CoreV1().ConfigMaps(namespace).Create(
					t.Context(),
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
				g.Expect(err).NotTo(HaveOccurred())
			},
			expectError:    false,
			expectedLength: 32,
		},
		{
			name:           "multiple configmaps",
			namespace:      "test-namespace",
			configMapNames: []string{"config1", "config2"},
			setupConfigMaps: func(client *fake.Clientset, namespace string, g Gomega) {
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
						t.Context(),
						cm,
						metav1.CreateOptions{},
					)
					g.Expect(err).NotTo(HaveOccurred())
				}
			},
			expectError:    false,
			expectedLength: 32,
		},
		{
			name:           "configmap with binary data",
			namespace:      "test-namespace",
			configMapNames: []string{"binary-config"},
			setupConfigMaps: func(client *fake.Clientset, namespace string, g Gomega) {
				_, err := client.CoreV1().ConfigMaps(namespace).Create(
					t.Context(),
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
				g.Expect(err).NotTo(HaveOccurred())
			},
			expectError:    false,
			expectedLength: 32,
		},
		{
			name:            "configmap not found",
			namespace:       "test-namespace",
			configMapNames:  []string{"non-existent"},
			setupConfigMaps: func(_ *fake.Clientset, _ string, _ Gomega) {},
			expectError:     true,
			expectedLength:  0,
		},
		{
			name:           "mixed existing and non-existing configmaps",
			namespace:      "test-namespace",
			configMapNames: []string{"existing", "non-existent"},
			setupConfigMaps: func(client *fake.Clientset, namespace string, g Gomega) {
				_, err := client.CoreV1().ConfigMaps(namespace).Create(
					t.Context(),
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
				g.Expect(err).NotTo(HaveOccurred())
			},
			expectError:    true,
			expectedLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientset()
			tt.setupConfigMaps(fakeClient, tt.namespace, g)

			checksum, err := CalculateConfigMapChecksum(
				t.Context(),
				fakeClient,
				tt.namespace,
				tt.configMapNames,
			)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())

			if tt.expectedLength == 0 {
				g.Expect(checksum).To(BeEmpty())
				return
			}

			g.Expect(checksum).To(HaveLen(tt.expectedLength))

			// Verify checksum is valid hex
			for _, char := range checksum {
				g.Expect((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')).To(
					BeTrue(),
					"checksum contains invalid hex character: %c",
					char,
				)
			}
		})
	}
}

func TestCalculateConfigMapChecksum_Consistency(t *testing.T) {
	g := NewWithT(t)
	fakeClient := fake.NewClientset()

	_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(
		t.Context(),
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
	g.Expect(err).NotTo(HaveOccurred())

	// Calculate checksum multiple times
	checksum1, err := CalculateConfigMapChecksum(
		t.Context(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	g.Expect(err).NotTo(HaveOccurred())

	checksum2, err := CalculateConfigMapChecksum(
		t.Context(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(checksum1).To(Equal(checksum2))
}

func TestCalculateConfigMapChecksum_Ordering(t *testing.T) {
	g := NewWithT(t)
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
			t.Context(),
			cm,
			metav1.CreateOptions{},
		)
		g.Expect(err).NotTo(HaveOccurred())
	}

	// Calculate checksum with different order
	checksum1, err := CalculateConfigMapChecksum(
		t.Context(),
		fakeClient,
		namespace,
		[]string{"config1", "config2"},
	)
	g.Expect(err).NotTo(HaveOccurred())

	checksum2, err := CalculateConfigMapChecksum(
		t.Context(),
		fakeClient,
		namespace,
		[]string{"config2", "config1"},
	)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(checksum1).To(Equal(checksum2))
}

func TestCalculateConfigMapChecksum_DataChanges(t *testing.T) {
	g := NewWithT(t)
	fakeClient := fake.NewClientset()

	_, err := fakeClient.CoreV1().ConfigMaps(namespace).Create(
		t.Context(),
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
	g.Expect(err).NotTo(HaveOccurred())

	checksum1, err := CalculateConfigMapChecksum(
		t.Context(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	g.Expect(err).NotTo(HaveOccurred())

	_, err = fakeClient.CoreV1().ConfigMaps(namespace).Update(
		t.Context(),
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
	g.Expect(err).NotTo(HaveOccurred())

	checksum2, err := CalculateConfigMapChecksum(
		t.Context(),
		fakeClient,
		namespace,
		[]string{"test-config"},
	)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(checksum1).NotTo(Equal(checksum2))
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
			g := NewWithT(t)
			result := calculateChecksum(tt.dataMaps...)

			if tt.expected == "" {
				g.Expect(result).To(BeEmpty())
				return
			}

			g.Expect(result).To(HaveLen(32))

			// Verify checksum is valid hex
			for _, char := range result {
				g.Expect((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')).To(
					BeTrue(),
					"checksum contains invalid hex character: %c",
					char,
				)
			}

			result2 := calculateChecksum(tt.dataMaps...)
			g.Expect(result).To(Equal(result2))
		})
	}
}

func TestCalculateChecksum_Ordering(t *testing.T) {
	g := NewWithT(t)
	map1 := map[string]any{"z": "26", "a": "1", "m": "13"}
	map2 := map[string]any{"a": "1", "m": "13", "z": "26"}

	result1 := calculateChecksum(map1)
	result2 := calculateChecksum(map2)

	g.Expect(result1).To(Equal(result2))

	result3 := calculateChecksum(map1, map2)
	result4 := calculateChecksum(map2, map1)

	g.Expect(result3).To(Equal(result4))
}

func TestCalculateChecksum_Deterministic(t *testing.T) {
	g := NewWithT(t)
	dataMap := map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	// Calculate checksum multiple times
	checksum1 := calculateChecksum(dataMap)
	checksum2 := calculateChecksum(dataMap)
	checksum3 := calculateChecksum(dataMap)

	g.Expect(checksum1).To(Equal(checksum2))
	g.Expect(checksum2).To(Equal(checksum3))
	g.Expect(checksum1).To(
		Equal(checksum3),
		"checksums should be identical across multiple calls: %s, %s, %s",
		checksum1, checksum2, checksum3,
	)
}
