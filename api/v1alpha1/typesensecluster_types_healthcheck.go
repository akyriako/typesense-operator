package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type HealthCheckSpec struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="akyriako78/typesense-healthcheck:0.1.8"
	Image string `json:"image,omitempty"`

	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

func (s *TypesenseClusterSpec) GetHealthCheckSidecarSpecs() HealthCheckSpec {
	if s.HealthCheck != nil {
		return *s.HealthCheck
	}

	return HealthCheckSpec{
		Image: "akyriako78/typesense-healthcheck:0.1.8",
	}
}

func (s *TypesenseClusterSpec) GetHealthCheckSidecarResources() corev1.ResourceRequirements {
	if s.HealthCheck != nil && s.HealthCheck.Resources != nil {
		return *s.HealthCheck.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
}
