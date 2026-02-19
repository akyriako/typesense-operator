package v1alpha1

import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type HttpRouteSpec struct {
	// +optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	Enabled bool `json:"enabled,omitempty"`

	Name string `json:"name"`

	ParentRef GatewayParentRef `json:"parentRef"`

	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Items:Pattern=`^(\*\.)?([a-z0-9]([-a-z0-9]*[a-z0-9])?)(\.([a-z0-9]([-a-z0-9]*[a-z0-9])?))*$`
	Hostnames []string `json:"hostnames,omitempty"`

	// +optional
	// +kubebuilder:default:="/"
	Path string `json:"path,omitempty"`

	// +optional
	// +kubebuilder:default:="PathPrefix"
	// +kubebuilder:validation:Enum=Exact;PathPrefix;ImplementationSpecific
	PathType *gatewayv1.PathMatchType `json:"pathType,omitempty"`

	//// +optional
	//// +kubebuilder:default=false
	//// +kubebuilder:validation:Type=boolean
	//UseReverseProxy *bool `json:"useReverseProxy,omitempty"`

	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// +optional
	ReferenceGrant *ReferenceGrantSpec `json:"referenceGrant,omitempty"`
}

type ReferenceGrantSpec struct {
	In   string `json:"in"`
	From string `json:"from"`
	To   string `json:"to"`
}

type GatewayParentRef struct {
	Name        string                 `json:"name"`
	Namespace   *gatewayv1.Namespace   `json:"namespace,omitempty"`
	SectionName *gatewayv1.SectionName `json:"section,omitempty"`
}
