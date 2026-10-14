package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type HealthCheckSpec struct {
	// Image specifies the Docker image to use for the Typesense health check sidecar container.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="quay.io/akyriako/typesense-healthcheck:0.2.0"
	Image string `json:"image,omitempty"`

	// Resources defines the compute resource requirements (CPU/Memory) for the health check sidecar container.
	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// LogLevel specifies the verbosity of the health check sidecar logs. Valid values are -4 (debug), 0 (info), 4 (warn), 8 (error).
	// +optional
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=-4
	// +kubebuilder:validation:Maximum=8
	// +kubebuilder:validation:ExclusiveMinimum=false
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:validation:Type=integer
	// +kubebuilder:validation:Enum=-4;0;4;8
	LogLevel int `json:"logLevel,omitempty"`
}

func (s *TypesenseClusterSpec) GetHealthCheckSidecarSpecs() HealthCheckSpec {
	if s.HealthCheck != nil {
		return *s.HealthCheck
	}

	return HealthCheckSpec{
		Image: "quay.io/akyriako/typesense-healthcheck:0.2.0",
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
