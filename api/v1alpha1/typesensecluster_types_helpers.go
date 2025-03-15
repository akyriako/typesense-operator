package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *TypesenseClusterSpec) GetResources() corev1.ResourceRequirements {
	if s.Resources != nil {
		return *s.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1024m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("128m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

func (s *TypesenseClusterSpec) GetAdditionalServerConfiguration() []corev1.EnvFromSource {
	if s.AdditionalServerConfiguration != nil {
		return []corev1.EnvFromSource{
			{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: *s.AdditionalServerConfiguration,
				},
			},
		}
	}

	return []corev1.EnvFromSource{}
}

func (s *TypesenseClusterSpec) GetCorsDomains() string {
	if s.CorsDomains == nil {
		return ""
	}
	return *s.CorsDomains
}

func (s *TypesenseClusterSpec) GetStorage() StorageSpec {
	if s.Storage != nil {
		return *s.Storage
	}

	return StorageSpec{
		Size:             resource.MustParse("100Mi"),
		StorageClassName: "standard",
	}
}

func (s *TypesenseClusterSpec) GetTopologySpreadConstraints(labels map[string]string) []corev1.TopologySpreadConstraint {
	tscs := make([]corev1.TopologySpreadConstraint, 0)

	for _, tsc := range s.TopologySpreadConstraints {
		if tsc.LabelSelector == nil {
			tsc.LabelSelector = &metav1.LabelSelector{
				MatchLabels: labels,
			}
		}
		tscs = append(tscs, tsc)
	}
	return tscs
}

func (s *TypesenseClusterSpec) GetMetricsExporterSpecs() MetricsExporterSpec {
	if s.Metrics != nil {
		return *s.Metrics
	}

	return MetricsExporterSpec{
		Release:           "promstack",
		Image:             "akyriako78/typesense-prometheus-exporter:0.1.7",
		IntervalInSeconds: 15,
	}
}
