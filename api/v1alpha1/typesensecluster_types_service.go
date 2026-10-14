package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
)

type ServiceSpec struct {
	// Type specifies the type of the Kubernetes Service exposing the Typesense cluster. Default is 'ClusterIP'.
	// +optional
	// +kubebuilder:default:="ClusterIP"
	// +kubebuilder:validation:Enum=ClusterIP;LoadBalancer
	Type corev1.ServiceType `json:"type"`

	// ExternalTrafficPolicy describes how nodes distribute service traffic they receive on one of the Service's externally-facing addresses.
	// +optional
	// +kubebuilder:validation:Enum=Cluster;Local
	ExternalTrafficPolicy *corev1.ServiceExternalTrafficPolicy `json:"externalTrafficPolicy,omitempty"`

	// Annotations allows you to attach custom annotations to the generated Kubernetes Service.
	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`
}
