package config

import (
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	konstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
)

func newTestReconciler(kubeClient *fake.Clientset) ConfigReconciler {
	return NewConfigReconciler(
		&alias.WorkloadClusterClient{Interface: kubeClient},
		365*24*time.Hour,
		90*24*time.Hour,
		"cluster.local",
		net.IPNet{
			IP:   net.IPv4(10, 96, 0, 0),
			Mask: net.CIDRMask(12, 32),
		},
		net.IPNet{
			IP:   net.IPv4(10, 244, 0, 0),
			Mask: net.CIDRMask(16, 32),
		},
		net.IPv4(10, 96, 0, 10),
		"kubeadm-config",
		"kube-system",
		"cluster-info",
		"kube-public",
		"kubelet-config",
		"kube-system",
	)
}

const expectedKubeletConfig = `apiVersion: kubelet.config.k8s.io/v1beta1
authentication:
  anonymous: {}
  webhook:
    cacheTTL: 0s
  x509:
    clientCAFile: /etc/kubernetes/pki/ca.crt
authorization:
  webhook:
    cacheAuthorizedTTL: 0s
    cacheUnauthorizedTTL: 0s
cgroupDriver: systemd
clusterDNS:
- 10.96.0.10
clusterDomain: cluster.local
containerRuntimeEndpoint: ""
cpuManagerReconcilePeriod: 0s
crashLoopBackOff: {}
evictionPressureTransitionPeriod: 0s
fileCheckFrequency: 0s
httpCheckFrequency: 0s
imageMaximumGCAge: 0s
imageMinimumGCAge: 0s
kind: KubeletConfiguration
logging:
  flushFrequency: 0
  options:
    json:
      infoBufferSize: "0"
    text:
      infoBufferSize: "0"
  verbosity: 0
memorySwap: {}
nodeStatusReportFrequency: 0s
nodeStatusUpdateFrequency: 0s
rotateCertificates: true
runtimeRequestTimeout: 0s
shutdownGracePeriod: 0s
shutdownGracePeriodCriticalPods: 0s
staticPodPath: /etc/kubernetes/manifests
streamingConnectionIdleTimeout: 0s
syncFrequency: 0s
volumeStatsAggPeriod: 0s
`

func TestReconcileKubeletConfig(t *testing.T) {
	g, ctx, _ := G(t)

	kubeClient := fake.NewClientset()
	reconciler := newTestReconciler(kubeClient)

	g.Expect(reconciler.ReconcileKubeletConfig(ctx)).To(Succeed())

	cm, err := kubeClient.CoreV1().ConfigMaps("kube-system").Get(ctx, "kubelet-config", metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cm.Data[konstants.KubeletBaseConfigurationConfigMapKey]).To(Equal(expectedKubeletConfig))
}
