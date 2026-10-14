package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

type SecurityContextSpec struct {

	// PodSecurityContext holds pod-level security attributes and common container settings.
	// +kubebuilder:validation:Optional
	PodSecurityContext *corev1.PodSecurityContext `json:"pod,omitempty"`

	// TypesenseSecurityContext holds security configuration that will be applied to the Typesense container.
	// +kubebuilder:validation:Optional
	TypesenseSecurityContext *corev1.SecurityContext `json:"typesense,omitempty"`

	// HealthcheckSecurityContext holds security configuration that will be applied to the health check sidecar container.
	// +kubebuilder:validation:Optional
	HealthcheckSecurityContext *corev1.SecurityContext `json:"healthcheck,omitempty"`

	// MetricsSecurityContext holds security configuration that will be applied to the metrics exporter sidecar container.
	// +kubebuilder:validation:Optional
	MetricsSecurityContext *corev1.SecurityContext `json:"metrics,omitempty"`
}

func (s *TypesenseClusterSpec) GetPodSecurityContext() *corev1.PodSecurityContext {
	if s.SecurityContext != nil && s.SecurityContext.PodSecurityContext != nil {
		return s.SecurityContext.PodSecurityContext
	}

	return &corev1.PodSecurityContext{
		RunAsUser:    ptr.To[int64](10000),
		FSGroup:      ptr.To[int64](2000),
		RunAsGroup:   ptr.To[int64](3000),
		RunAsNonRoot: ptr.To[bool](true),
	}
}

func (s *TypesenseClusterSpec) GetTypesenseSecurityContext() *corev1.SecurityContext {
	if s.SecurityContext == nil {
		return nil
	}

	return s.SecurityContext.TypesenseSecurityContext
}

func (s *TypesenseClusterSpec) GetHealthcheckSecurityContext() *corev1.SecurityContext {
	if s.SecurityContext == nil {
		return nil
	}

	return s.SecurityContext.HealthcheckSecurityContext
}

func (s *TypesenseClusterSpec) GetMetricsSecurityContext() *corev1.SecurityContext {
	if s.SecurityContext == nil {
		return nil
	}

	return s.SecurityContext.MetricsSecurityContext
}
