package alias

import "k8s.io/client-go/kubernetes"

type WorkloadClusterClient struct {
	kubernetes.Interface
}
type ManagementClusterClient struct {
	kubernetes.Interface
}
