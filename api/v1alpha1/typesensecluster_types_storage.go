package v1alpha1

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type StorageSpec struct {

	// +optional
	// +kubebuilder:default="100Mi"
	Size resource.Quantity `json:"size,omitempty"`

	StorageClassName string `json:"storageClassName"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany
	// +kubebuilder:default:=ReadWriteOnce
	AccessMode string `json:"accessMode,omitempty"`

	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// +kubebuilder:validation:Optional
	RetentionPolicy *appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy `json:"retentionPolicy"`
}

func (s *TypesenseClusterSpec) GetStorage() StorageSpec {
	if s.Storage != nil {
		return *s.Storage
	}

	return StorageSpec{
		Size:             resource.MustParse("100Mi"),
		StorageClassName: "standard",
		AccessMode:       "ReadWriteOnce",
		RetentionPolicy: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
			WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
			WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
		},
	}
}

func (s *TypesenseClusterSpec) GetStorageRetentionPolicy() *appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy {
	if s.Storage != nil && s.Storage.RetentionPolicy != nil {
		return s.Storage.RetentionPolicy
	}

	return &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
		WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
		WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
	}
}
