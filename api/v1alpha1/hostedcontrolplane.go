package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func (s *HostedControlPlaneSpec) ReplicasOrDefault() int32 {
	defaultReplicas := int32(2)
	if s == nil {
		return defaultReplicas
	}
	return ptr.Deref(s.Replicas, defaultReplicas)
}

func (e *ETCDComponent) AutoGrowEnabled() bool {
	return e == nil || ptr.Deref(e.AutoGrow, true)
}

func (kp *KubeProxyComponent) Enabled() bool {
	return kp == nil || !ptr.Deref(kp.Disabled, false)
}

func (awc *AuditWebhookAuthentication) TokenKeyOrDefault() string {
	defaultTokenKey := "token"
	if awc == nil {
		return defaultTokenKey
	}
	return ptr.Deref(awc.TokenKey, defaultTokenKey)
}

func (sp *ScalablePod) ReplicaCount(defaultReplicas int32) int32 {
	if sp == nil {
		return defaultReplicas
	}
	return ptr.Deref(sp.Replicas, defaultReplicas)
}

func (c *Container) ImagePullPolicyOrDefault() corev1.PullPolicy {
	defaultPullPolicy := corev1.PullAlways
	if c == nil {
		return defaultPullPolicy
	}
	return ptr.Deref(c.ImagePullPolicy, defaultPullPolicy)
}

func (a *Audit) ModeOrDefault() string {
	defaultMode := "batch"
	if a == nil {
		return defaultMode
	}
	return ptr.Deref(a.Mode, defaultMode)
}

func (ebs *ETCDBackupSecret) AccessKeyIDKeyOrDefault() string {
	defaultKey := "accessKeyID"
	if ebs == nil {
		return defaultKey
	}
	return ptr.Deref(ebs.AccessKeyIDKey, defaultKey)
}

func (ebs *ETCDBackupSecret) SecretAccessKeyKeyOrDefault() string {
	defaultKey := "secretAccessKey"
	if ebs == nil {
		return defaultKey
	}
	return ptr.Deref(ebs.SecretAccessKeyKey, defaultKey)
}
