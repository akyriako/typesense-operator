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

// TypesenseClusterSpec defines the desired state of TypesenseCluster
type TypesenseClusterSpec struct {
	// Image specifies the Typesense Docker image to use for the cluster nodes.
	// +kubebuilder:default:="typesense/typesense:30.2"
	Image string `json:"image"`

	// ImagePullSecrets is an optional list of references to secrets in the same namespace to use for pulling any of the images.
	// +kubebuilder:validation:Optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// AdminApiKey references a Secret containing the Typesense admin API key (key: typesense-api-key). If not provided, the operator generates a random key automatically.
	AdminApiKey *corev1.SecretReference `json:"adminApiKey,omitempty"`

	// Replicas defines the number of Typesense nodes in the cluster. For high availability and Raft consensus, 3 or 5 nodes are recommended.
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=7
	// +kubebuilder:validation:ExclusiveMinimum=false
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:validation:Type=integer
	// +kubebuilder:validation:Enum=1;3;5;7
	Replicas int32 `json:"replicas,omitempty"`

	// ApiPort specifies the port on which the Typesense API is exposed.
	// +optional
	// +kubebuilder:default=8108
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:validation:ExclusiveMinimum=true
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:validation:Type=integer
	ApiPort int `json:"apiPort,omitempty"`

	// PeeringPort specifies the port used for intra-cluster communication (Raft consensus) between Typesense nodes.
	// +optional
	// +kubebuilder:default=8107
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:validation:ExclusiveMinimum=true
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:validation:Type=integer
	PeeringPort int `json:"peeringPort,omitempty"`

	// HealthProbeTimeoutInMilliseconds defines how long the operator waits for a node's /status and /health
	// endpoints to respond before treating the node as unreachable or unhealthy.
	// +optional
	// +kubebuilder:default=500
	// +kubebuilder:validation:Minimum=500
	// +kubebuilder:validation:Maximum=60000
	// +kubebuilder:validation:ExclusiveMinimum=false
	// +kubebuilder:validation:ExclusiveMaximum=false
	// +kubebuilder:validation:Type=integer
	HealthProbeTimeoutInMilliseconds int `json:"healthProbeTimeoutInMilliseconds,omitempty"`

	// ResetPeersOnError indicates whether the cluster should automatically attempt to reset peering connections if quorum is lost.
	// +optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	ResetPeersOnError bool `json:"resetPeersOnError,omitempty"`

	// EnableCors enables CORS (Cross-Origin Resource Sharing) for the Typesense API, allowing browsers to make direct requests.
	// +optional
	// +kubebuilder:default=false
	// +kubebuilder:validation:Type=boolean
	EnableCors bool `json:"enableCors,omitempty"`

	// CorsDomains specifies a comma-separated list of allowed domains for CORS if EnableCors is true.
	// +optional
	// +kubebuilder:validation:Type=string
	CorsDomains *string `json:"corsDomains,omitempty"`

	// ForceResetPeersConfigOnUpdate forces the operator to rewrite the peers configuration during updates.
	// +optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:Type=boolean
	ForceResetPeersConfigOnUpdate bool `json:"forceResetPeersConfigOnUpdate,omitempty"`

	// Resources defines the compute resource requirements (CPU/Memory) for the Typesense pods.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Affinity configures pod scheduling constraints (e.g., node affinity or pod anti-affinity).
	// +kubebuilder:validation:Optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// NodeSelector restricts the Typesense pods to run only on nodes with the specified labels.
	// +kubebuilder:validation:Optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allows the Typesense pods to be scheduled on nodes with matching taints.
	// +kubebuilder:validation:Optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// AdditionalServerConfiguration references a ConfigMap containing extra Typesense configuration flags via environment variables.
	// +kubebuilder:validation:Optional
	AdditionalServerConfiguration *corev1.LocalObjectReference `json:"additionalServerConfiguration,omitempty"`

	// ServiceAnnotations allows you to attach custom annotations to the generated Typesense Services.
	// +kubebuilder:validation:Optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// StatefulSetAnnotations allows you to attach custom annotations to the generated StatefulSet.
	// +kubebuilder:validation:Optional
	StatefulSetAnnotations map[string]string `json:"statefulSetAnnotations,omitempty"`

	// PodAnnotations allows you to attach custom annotations to the generated Typesense Pods.
	// +kubebuilder:validation:Optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// PodsInheritStatefulSetAnnotations defines whether the Pods should automatically inherit annotations added to the StatefulSet.
	// +optional
	// +kubebuilder:default=false
	// +kubebuilder:validation:Type=boolean
	PodsInheritStatefulSetAnnotations bool `json:"podsInheritStatefulSetAnnotations,omitempty"`

	// Storage configures the PersistentVolumeClaims for the Typesense nodes. This is where the search data is persisted.
	Storage *StorageSpec `json:"storage"`

	// Ingress configures external access to the Typesense API, optionally setting up a reverse proxy (e.g. NGINX) in front of the cluster.
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// Service allows customization of the Kubernetes Service exposing the Typesense cluster.
	Service *ServiceSpec `json:"service,omitempty"`

	// HttpRoutes allows you to configure Gateway API HTTPRoutes for routing external traffic to the Typesense cluster.
	HttpRoutes []HttpRouteSpec `json:"httpRoutes,omitempty"`

	// Scrapers configures DocSearch scrapers to automatically crawl websites and index their content into this Typesense cluster.
	Scrapers []DocSearchScraperSpec `json:"scrapers,omitempty"`

	// Metrics configures the Prometheus exporter sidecar for collecting and exposing Typesense metrics.
	Metrics *MetricsExporterSpec `json:"metrics,omitempty"`

	// HealthCheck allows customizing the health check sidecar container.
	HealthCheck *HealthCheckSpec `json:"healthcheck,omitempty"`

	// TopologySpreadConstraints controls how Pods are spread across your cluster among failure-domains such as regions, zones, nodes, and other user-defined topology domains.
	// +kubebuilder:validation:Optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// PriorityClassName indicates the importance of these Pods relative to other Pods in the cluster.
	// +kubebuilder:validation:Optional
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// SecurityContext defines the security attributes (e.g., runAsUser, fsGroup) for the Pods and containers.
	SecurityContext *SecurityContextSpec `json:"securityContext,omitempty"`

	// IgnoreAnnotationsFromExternalMutations is a list of annotation keys that the operator should ignore if they are modified by external tools (e.g., sidecar injectors).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Items:Type=string
	IgnoreAnnotationsFromExternalMutations []string `json:"ignoreAnnotationsFromExternalMutations,omitempty"`

	// IncrementalQuorumRecovery attempts to safely recover a degraded cluster by incrementally scaling up and replacing broken nodes.
	// +optional
	// +kubebuilder:default=false
	// +kubebuilder:validation:Type=boolean
	IncrementalQuorumRecovery bool `json:"incrementalQuorumRecovery,omitempty"`
}

// TypesenseClusterStatus defines the observed state of TypesenseCluster
type TypesenseClusterStatus struct {

	// Conditions represents the latest available observations of the cluster's current state.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// Phase represents the current lifecycle state of the Typesense cluster (e.g., Initializing, QuorumReady, Degraded).
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration represents the most recent generation observed by the controller.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TypesenseCluster is the Schema for the typesenseclusters API
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="API Port",type=integer,JSONPath=`.spec.apiPort`
// +kubebuilder:printcolumn:name="Peering Port",type=integer,JSONPath=`.spec.peeringPort`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
type TypesenseCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of the Typesense cluster.
	Spec TypesenseClusterSpec `json:"spec,omitempty"`
	// Status represents the observed state of the Typesense cluster.
	Status TypesenseClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TypesenseClusterList contains a list of TypesenseCluster
type TypesenseClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items is a list of TypesenseCluster resources.
	Items []TypesenseCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TypesenseCluster{}, &TypesenseClusterList{})
}
