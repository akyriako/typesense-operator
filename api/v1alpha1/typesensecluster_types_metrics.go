package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type MetricsExporterSpec struct {
	Release string `json:"release"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="akyriako78/typesense-prometheus-exporter:0.1.9"
	Image string `json:"image,omitempty"`

	// +optional
	// +kubebuilder:default=15
	// +kubebuilder:validation:Minimum=15
	// +kubebuilder:validation:Maximum=60
	// +kubebuilder:validation:ExclusiveMinimum=false
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:validation:Type=integer
	IntervalInSeconds int `json:"interval,omitempty"`

	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

func (s *TypesenseClusterSpec) GetMetricsExporterSpecs() MetricsExporterSpec {
	if s.Metrics != nil {
		return *s.Metrics
	}

	return MetricsExporterSpec{
		Release:           "promstack",
		Image:             "akyriako78/typesense-prometheus-exporter:0.1.9",
		IntervalInSeconds: 15,
	}
}

func (s *TypesenseClusterSpec) GetMetricsExporterResources() corev1.ResourceRequirements {
	if s.Metrics != nil && s.Metrics.Resources != nil {
		return *s.Metrics.Resources
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
