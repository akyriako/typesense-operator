package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type IngressSpec struct {
	// Referer specifies an optional referer restriction for the Ingress. Only requests with the matching Referer header will be allowed.
	// +optional
	// +kubebuilder:validation:Pattern:=`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9]))*$`
	Referer *string `json:"referer,omitempty"`

	// Host specifies the fully qualified domain name (FQDN) used for routing external traffic to the Ingress.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern:=`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9]))*$`
	Host string `json:"host"`

	// HttpDirectives allows injecting custom NGINX HTTP-level directives into the reverse proxy configuration.
	HttpDirectives *string `json:"httpDirectives,omitempty"`

	// ServerDirectives allows injecting custom NGINX Server-level directives into the reverse proxy configuration.
	ServerDirectives *string `json:"serverDirectives,omitempty"`

	// LocationDirectives allows injecting custom NGINX Location-level directives into the reverse proxy configuration.
	LocationDirectives *string `json:"locationDirectives,omitempty"`

	// ClusterIssuer specifies the name of a cert-manager ClusterIssuer to automatically provision TLS certificates.
	// +optional
	ClusterIssuer *string `json:"clusterIssuer,omitempty"`

	// Replicas defines the number of reverse proxy (NGINX) pods to run.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// Config allows you to completely override the generated nginx.conf. You can use Go template variables like {{.ServiceName}} and {{.ServicePort}}.
	// +optional
	Config *string `json:"config,omitempty"`

	// IngressClassName specifies the name of the IngressClass to use for the Ingress resource.
	IngressClassName string `json:"ingressClassName"`

	// ServiceAnnotations allows you to attach custom annotations to the generated reverse proxy Service.
	// +kubebuilder:validation:Optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// Labels allows you to attach custom labels to the generated Ingress and reverse proxy resources.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations allows you to attach custom annotations to the generated Ingress resource.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// TLSSecretName specifies the name of the Kubernetes Secret containing the TLS certificate and key for the Ingress.
	// +optional
	TLSSecretName *string `json:"tlsSecretName,omitempty"`

	// Resources defines the compute resource requirements (CPU/Memory) for the reverse proxy container.
	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Image specifies the Docker image to use for the reverse proxy container. Default is 'nginx:alpine'.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="nginx:alpine"
	Image string `json:"image,omitempty"`

	// Command allows you to override the default entrypoint command for the reverse proxy container.
	// +optional
	Command []string `json:"command,omitempty"`

	// ReadOnlyRootFilesystem indicates whether the reverse proxy container should run with a read-only root filesystem.
	// +optional
	// +kubebuilder:default:=false
	ReadOnlyRootFilesystem bool `json:"readOnlyRootFilesystem,omitempty"`

	// SecurityContext defines the security attributes for the reverse proxy container.
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// Volumes allows you to define additional volumes for the reverse proxy pod.
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts allows you to mount the additional volumes into the reverse proxy container.
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Path specifies the URL path to match for routing traffic. Default is '/'.
	// +optional
	// +kubebuilder:default:="/"
	Path string `json:"path,omitempty"`

	// PathType determines how the Path is matched against the incoming request URL. Default is 'ImplementationSpecific'.
	// +optional
	// +kubebuilder:default:="ImplementationSpecific"
	// +kubebuilder:validation:Enum=Exact;Prefix;ImplementationSpecific
	PathType *networkingv1.PathType `json:"pathType,omitempty"`

	// ServiceType specifies the type of the Kubernetes Service exposing the reverse proxy. Default is 'NodePort'.
	// +kubebuilder:default:=NodePort
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`
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
