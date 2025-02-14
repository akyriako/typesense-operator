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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TypesenseKeyRequestSpec defines the desired state of TypesenseKeyRequest
type TypesenseKeyRequestSpec struct {
	Cluster corev1.ObjectReference `json:"cluster"`

	ApiKeySecret *corev1.SecretReference `json:"apiKeySecret,omitempty"`

	// +kubebuilder:default:="autogenerated key request"
	Description string `json:"description"`

	// +kubebuilder:validation:MinLength=1
	Actions string `json:"actions"`

	// +kubebuilder:validation:MinLength=1
	Collections string `json:"collections"`
}

// TypesenseKeyRequestStatus defines the observed state of TypesenseKeyRequest
type TypesenseKeyRequestStatus struct {

	// +kubebuilder:default=false
	Ready bool `json:"ready,omitempty"`

	KeyId *uint32 `json:"keyId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TypesenseKeyRequest is the Schema for the typesensekeyrequests API
// +kubebuilder:printcolumn:name="Actions",type=string,JSONPath=`.spec.actions`
// +kubebuilder:printcolumn:name="Collections",type=string,JSONPath=`.spec.collections`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Key ID",type=string,JSONPath=`.status.keyId`
type TypesenseKeyRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TypesenseKeyRequestSpec   `json:"spec,omitempty"`
	Status TypesenseKeyRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TypesenseKeyRequestList contains a list of TypesenseKeyRequest
type TypesenseKeyRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TypesenseKeyRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TypesenseKeyRequest{}, &TypesenseKeyRequestList{})
}
