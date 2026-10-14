package v1alpha1

import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type HttpRouteSpec struct {
	// Enabled specifies whether to create the HTTPRoute resource. Default is true.
	// +optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	Enabled bool `json:"enabled,omitempty"`

	// Name specifies the name of the HTTPRoute resource.
	Name string `json:"name"`

	// ParentRef references the Gateway (or other parent resource) that this route should attach to.
	ParentRef GatewayParentRef `json:"parentRef"`

	// Hostnames defines a set of hostnames that should match against the HTTP Host header to select this route.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Items:Pattern=`^(\*\.)?([a-z0-9]([-a-z0-9]*[a-z0-9])?)(\.([a-z0-9]([-a-z0-9]*[a-z0-9])?))*$`
	Hostnames []string `json:"hostnames,omitempty"`

	// Path specifies the URL path to match for routing traffic to the Typesense cluster. Default is '/'.
	// +optional
	// +kubebuilder:default:="/"
	Path string `json:"path,omitempty"`

	// PathType determines how the Path is matched against the incoming request URL. Default is 'PathPrefix'.
	// +optional
	// +kubebuilder:default:="PathPrefix"
	// +kubebuilder:validation:Enum=Exact;PathPrefix;ImplementationSpecific
	PathType *gatewayv1.PathMatchType `json:"pathType,omitempty"`

	// Labels allows you to attach custom labels to the generated HTTPRoute.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations allows you to attach custom annotations to the generated HTTPRoute.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// ReferenceGrant enables the creation of a ReferenceGrant if the parent Gateway resides in a different namespace.
	// +optional
	// +kubebuilder:default=false
	// +kubebuilder:validation:Type=boolean
	ReferenceGrant *bool `json:"referenceGrant,omitempty"`
}

type GatewayParentRef struct {
	// Name is the name of the referent Gateway.
	Name string `json:"name"`
	// Namespace is the namespace of the referent Gateway.
	Namespace *gatewayv1.Namespace `json:"namespace,omitempty"`
	// SectionName is the name of a section within the target Gateway (e.g. a specific listener).
	SectionName *gatewayv1.SectionName `json:"section,omitempty"`
}
