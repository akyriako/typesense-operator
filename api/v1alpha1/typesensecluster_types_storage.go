package v1alpha1

import "k8s.io/apimachinery/pkg/api/resource"

type StorageSpec struct {

	// Size specifies the capacity of the PersistentVolumeClaim (e.g., 10Gi, 100Gi).
	// +optional
	// +kubebuilder:default="1Gi"
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName specifies the name of the StorageClass to use. Leave empty to use the cluster's default StorageClass.
	// +optional
	// +kubebuilder:default:=""
	StorageClassName string `json:"storageClassName,omitempty"`

	// AccessMode determines the access mode for the PersistentVolumeClaim. Usually ReadWriteOnce.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany
	// +kubebuilder:default:=ReadWriteOnce
	AccessMode string `json:"accessMode,omitempty"`

	// Annotations allows you to attach custom annotations to the generated PersistentVolumeClaims.
	Annotations map[string]string `json:"annotations,omitempty"`
}

func (s *TypesenseClusterSpec) GetStorage() StorageSpec {
	if s.Storage != nil {
		return *s.Storage
	}

	return StorageSpec{
		Size:             resource.MustParse("1Gi"),
		StorageClassName: "standard",
		AccessMode:       "ReadWriteOnce",
	}
}
