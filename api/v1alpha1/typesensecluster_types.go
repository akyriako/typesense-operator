/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TypesenseClusterSpec defines the desired state of TypesenseCluster
type TypesenseClusterSpec struct {
	Image string `json:"image"`

	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:ExclusiveMinimum=false
	// +kubebuilder:validation:Type=integer
	Replicas int32 `json:"replicas,omitempty"`

	// +optional
	// +kubebuilder:default=8108
	// +kubebuilder:validation:Type=integer
	ApiPort int `json:"apiPort,omitempty"`

	// +optional
	// +kubebuilder:default=8107
	// +kubebuilder:validation:Type=integer
	PeeringPort int `json:"peeringPort,omitempty"`

	// +optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	ResetPeersOnError bool `json:"resetPeersOnError,omitempty"`

	// +optional
	// +kubebuilder:validation:Type=string
	CorsDomains *string `json:"corsDomains,omitempty"`

	Storage *StorageSpec `json:"storage"`

	Ingress *IngressSpec `json:"ingress,omitempty"`
}

type StorageSpec struct {

	// +optional
	// +kubebuilder:default="100Mi"
	Size resource.Quantity `json:"size,omitempty"`

	StorageClassName string `json:"storageClassName"`
}

type IngressSpec struct {
	// +optional
	// +kubebuilder:validation:Pattern:=`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9]))*$`
	Referer *string `json:"referer,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern:=`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9]))*$`
	Host string `json:"host"`

	ClusterIssuer string `json:"clusterIssuer"`

	IngressClassName string `json:"ingressClassName"`

	Annotations map[string]string `json:"annotations,omitempty"`
}

// TypesenseClusterStatus defines the observed state of TypesenseCluster
type TypesenseClusterStatus struct {

	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TypesenseCluster is the Schema for the typesenseclusters API
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="API Port",type=integer,JSONPath=`.spec.apiPort`
// +kubebuilder:printcolumn:name="Peering Port",type=integer,JSONPath=`.spec.peeringPort`
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
type TypesenseCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TypesenseClusterSpec   `json:"spec,omitempty"`
	Status TypesenseClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TypesenseClusterList contains a list of TypesenseCluster
type TypesenseClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TypesenseCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TypesenseCluster{}, &TypesenseClusterList{})
}
