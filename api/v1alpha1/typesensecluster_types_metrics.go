package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type MetricsExporterSpec struct {
	// Release specifies the Prometheus Operator release label to which the generated PodMonitor will be attached.
	Release string `json:"release"`

	// Image specifies the Docker image to use for the Typesense Prometheus exporter sidecar container.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="quay.io/akyriako/typesense-prometheus-exporter:0.1.9"
	Image string `json:"image,omitempty"`

	// IntervalInSeconds specifies the scraping interval for the Prometheus exporter.
	// +optional
	// +kubebuilder:default=15
	// +kubebuilder:validation:Minimum=15
	// +kubebuilder:validation:Maximum=60
	// +kubebuilder:validation:ExclusiveMinimum=false
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:validation:Type=integer
	IntervalInSeconds int `json:"interval,omitempty"`

	// Resources defines the compute resource requirements (CPU/Memory) for the Prometheus exporter sidecar container.
	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// LogLevel specifies the verbosity of the Prometheus exporter sidecar logs. Valid values are -4 (debug), 0 (info), 4 (warn), 8 (error).
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

func (s *TypesenseClusterSpec) GetMetricsExporterSpecs() MetricsExporterSpec {
	if s.Metrics != nil {
		return *s.Metrics
	}

	return MetricsExporterSpec{
		Release:           "promstack",
		Image:             "quay.io/akyriako/typesense-prometheus-exporter:0.1.9",
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
