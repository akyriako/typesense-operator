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
			corev1.ResourceCPU:    resource.MustParse("1000m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
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

func (s *TypesenseClusterSpec) GetAffinity(labels map[string]string) *corev1.Affinity {
	if s.Affinity == nil {
		return nil
	}

	affinity := s.Affinity.DeepCopy()

	if affinity.PodAffinity != nil {
		for _, term := range affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
			setPodAffinityTermLabels(&term, labels)
		}

		for _, weightedTerm := range affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
			setPodAffinityTermLabels(&weightedTerm.PodAffinityTerm, labels)
		}
	}

	if affinity.PodAntiAffinity != nil {
		for _, term := range affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
			setPodAffinityTermLabels(&term, labels)
		}

		for _, weightedTerm := range affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
			setPodAffinityTermLabels(&weightedTerm.PodAffinityTerm, labels)
		}
	}

	return affinity
}

func (s *TypesenseClusterSpec) GetTopologySpreadConstraints(labels map[string]string) []corev1.TopologySpreadConstraint {
	tscs := make([]corev1.TopologySpreadConstraint, 0)

	for _, tsc := range s.TopologySpreadConstraints {
		if tsc.LabelSelector == nil {
			tsc.LabelSelector = &metav1.LabelSelector{
				MatchLabels: cloneLabels(labels),
			}
		}
		tscs = append(tscs, tsc)
	}
	return tscs
}

func setPodAffinityTermLabels(term *corev1.PodAffinityTerm, labels map[string]string) {
	if term.LabelSelector == nil {
		term.LabelSelector = &metav1.LabelSelector{
			MatchLabels: cloneLabels(labels),
		}
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}
