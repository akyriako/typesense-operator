package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
)

type ServiceSpec struct {
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// +optional
	// +kubebuilder:default:="ClusterIP"
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer;ExternalName
	Type corev1.ServiceType `json:"type"`

	// +optional
	// +kubebuilder:validation:Enum=Cluster;Local
	InternalTrafficPolicy *corev1.ServiceInternalTrafficPolicy `json:"internalTrafficPolicy,omitempty"`

	// +optional
	// +kubebuilder:default:="Cluster"
	// +kubebuilder:validation:Enum=Cluster;Local
	ExternalTrafficPolicy corev1.ServiceExternalTrafficPolicy `json:"externalTrafficPolicy,omitempty"`
}
