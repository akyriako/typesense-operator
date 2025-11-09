package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type IngressSpec struct {
	// +optional
	// +kubebuilder:validation:Pattern:=`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9]))*$`
	Referer *string `json:"referer,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern:=`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9]))*$`
	Host string `json:"host"`

	HttpDirectives     *string `json:"httpDirectives,omitempty"`
	ServerDirectives   *string `json:"serverDirectives,omitempty"`
	LocationDirectives *string `json:"locationDirectives,omitempty"`

	// +optional
	ClusterIssuer *string `json:"clusterIssuer,omitempty"`

	IngressClassName string `json:"ingressClassName"`

	Annotations map[string]string `json:"annotations,omitempty"`

	// +optional
	TLSSecretName *string `json:"tlsSecretName,omitempty"`

	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="nginx:alpine"
	Image string `json:"image,omitempty"`

	// +optional
	ReadOnlyRootFilesystem *ReadOnlyRootFilesystemSpec `json:"readOnlyRootFilesystem,omitempty"`

	// +optional
	// +kubebuilder:default:="/"
	Path string `json:"path,omitempty"`

	// +optional
	// +kubebuilder:default:="ImplementationSpecific"
	// +kubebuilder:validation:Enum=Exact;Prefix;ImplementationSpecific
	PathType *networkingv1.PathType `json:"pathType,omitempty"`
}

type ReadOnlyRootFilesystemSpec struct {
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`
}

func (s *IngressSpec) GetReverseProxyResources() corev1.ResourceRequirements {
	if s.Resources != nil {
		return *s.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("150m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
}
