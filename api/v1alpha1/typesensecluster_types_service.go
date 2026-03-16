package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
)

type ServiceSpec struct {
	// +optional
	// +kubebuilder:default:="ClusterIP"
	// +kubebuilder:validation:Enum=ClusterIP;LoadBalancer
	Type corev1.ServiceType `json:"type"`

	// +optional
	// +kubebuilder:default:="Cluster"
	// +kubebuilder:validation:Enum=Cluster;Local
	ExternalTrafficPolicy corev1.ServiceExternalTrafficPolicy `json:"externalTrafficPolicy,omitempty"`
}
