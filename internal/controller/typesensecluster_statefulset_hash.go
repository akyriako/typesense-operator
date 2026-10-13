package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/mitchellh/hashstructure/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type UpdateStatefulSetTrigger string

var (
	SpecReplicasChanged             UpdateStatefulSetTrigger = "SpecReplicasChanged"
	HashAnnotationChanged           UpdateStatefulSetTrigger = "HashAnnotationChanged"
	PodAnnotationsChanged           UpdateStatefulSetTrigger = "PodAnnotationsChanged"
	StatefulSetAnnotationsChanged   UpdateStatefulSetTrigger = "StatefulSetAnnotationsChanged"
	SpecResourcesChanged            UpdateStatefulSetTrigger = "SpecResourcesChanged"
	PodSecurityContextChanged       UpdateStatefulSetTrigger = "PodSecurityContextChanged"
	InvalidContainerCount           UpdateStatefulSetTrigger = "InvalidContainerCount"
	ContainerSecurityContextChanged UpdateStatefulSetTrigger = "ContainerSecurityContextChanged"
	SpecTypesenseVersionChanged     UpdateStatefulSetTrigger = "SpecTypesenseVersionChanged"
)

func (r *TypesenseClusterReconciler) shouldUpdateStatefulSet(sts *appsv1.StatefulSet, desired *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) (update bool, scaleOnly bool, triggers []UpdateStatefulSetTrigger) {
	update = false
	scaleOnly = false

	if sts == nil || ts == nil || desired == nil {
		return false, false, nil
	}

	condition := r.getConditionReady(ts)
	if condition == nil {
		return false, false, nil
	}

	// SpecReplicasChanged
	if ptr.Deref(sts.Spec.Replicas, 1) != ts.Spec.Replicas {
		isScaleDown := ts.Spec.Replicas < ptr.Deref(sts.Spec.Replicas, 1)
		if isScaleDown || !isQuorumRecoveryState(condition.Reason) {
			triggers = append(triggers, SpecReplicasChanged)
			if isScaleDown {
				update = true
			} else {
				update = false
				scaleOnly = true
			}
		}
	}

	// HashAnnotationChanged
	if sts.Spec.Template.Annotations[hashAnnotationKey] != desired.Spec.Template.Annotations[hashAnnotationKey] {
		triggers = append(triggers, HashAnnotationChanged)
		update = true
	}

	// Externally mutated annotations are ignored, unless the operator sets them itself (e.g. a pod annotation
	// that also matches an ignore filter); filtering those as well would report a change on every reconcile,
	// and since every update stamps restartedAt, the pods would be restarted continuously.
	mutatedAnnotations := ts.Spec.IgnoreAnnotationsFromExternalMutations
	stsFilters := append([]string{rancherDomainAnnotationKey}, mutatedAnnotations...)
	podFilters := append([]string{restartPodsAnnotationKey, rancherDomainAnnotationKey}, mutatedAnnotations...)
	stsAnnotations := withoutExternalKeys(sts.Annotations, desired.Annotations, stsFilters...)
	podAnnotations := withoutExternalKeys(sts.Spec.Template.Annotations, desired.Spec.Template.Annotations, podFilters...)

	// PodAnnotationsChanged
	if !apiequality.Semantic.DeepEqual(podAnnotations, desired.Spec.Template.Annotations) {
		triggers = append(triggers, PodAnnotationsChanged)
		update = true
	}

	// StatefulSetAnnotationsChanged
	if !apiequality.Semantic.DeepEqual(stsAnnotations, desired.Annotations) {
		triggers = append(triggers, StatefulSetAnnotationsChanged)
		update = true
	}

	// SpecResourcesChanged
	// if !apiequality.Semantic.DeepEqual(sts.Spec.Template.Spec.Containers[0].Resources, ts.Spec.GetResources()) {
	// 	triggers = append(triggers, SpecResourcesChanged)
	// 	update = true
	// }

	// PodSecurityContextChanged
	if !apiequality.Semantic.DeepEqual(sts.Spec.Template.Spec.SecurityContext, ts.Spec.GetPodSecurityContext()) {
		triggers = append(triggers, PodSecurityContextChanged)
		update = true
	}

	// InvalidContainerCount
	if len(sts.Spec.Template.Spec.Containers) < 3 {
		triggers = append(triggers, InvalidContainerCount)
		update = true
		return update, scaleOnly, triggers
	}

	// ContainerSecurityContextChanged
	if !apiequality.Semantic.DeepEqual(sts.Spec.Template.Spec.Containers[0].SecurityContext, ts.Spec.GetTypesenseSecurityContext()) {
		triggers = append(triggers, ContainerSecurityContextChanged)
		update = true
	}

	// ContainerSecurityContextChanged
	if !apiequality.Semantic.DeepEqual(sts.Spec.Template.Spec.Containers[1].SecurityContext, ts.Spec.GetMetricsSecurityContext()) {
		triggers = append(triggers, ContainerSecurityContextChanged)
		update = true
	}

	// ContainerSecurityContextChanged
	if !apiequality.Semantic.DeepEqual(sts.Spec.Template.Spec.Containers[2].SecurityContext, ts.Spec.GetHealthcheckSecurityContext()) {
		triggers = append(triggers, ContainerSecurityContextChanged)
		update = true
	}

	return update, scaleOnly, triggers
}

func (r *TypesenseClusterReconciler) shouldEmergencyUpdateStatefulSet(sts *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) bool {
	if sts == nil || ts == nil {
		return false
	}

	condition := r.getConditionReady(ts)
	if condition == nil {
		return false
	}

	if ptr.Deref(sts.Spec.Replicas, 1) != ts.Spec.Replicas {
		isScaleDown := ts.Spec.Replicas < ptr.Deref(sts.Spec.Replicas, 1)
		if isScaleDown || !isQuorumRecoveryState(condition.Reason) {
			return true
		}
	}

	// InvalidContainerCount
	if len(sts.Spec.Template.Spec.Containers) < 3 {
		return true
	}

	return false
}

func (r *TypesenseClusterReconciler) buildStatefulSetHash(ctx context.Context, sts *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) (*string, error) {
	stsTemplate := sts.Spec.Template.DeepCopy()
	if stsTemplate.Annotations == nil {
		stsTemplate.Annotations = map[string]string{}
	}

	delete(stsTemplate.Annotations, hashAnnotationKey)
	podTemplate := stsTemplate.Spec.DeepCopy()

	var additionalConfData map[string]map[string]string
	if additionalConfiguration := ts.Spec.GetAdditionalServerConfiguration(); additionalConfiguration != nil {
		additionalConfData = make(map[string]map[string]string)
		for _, ac := range additionalConfiguration {
			if ac.ConfigMapRef != nil {
				configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: ac.ConfigMapRef.Name}
				var cm = &corev1.ConfigMap{}
				if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
					return nil, err
				}

				additionalConfData[cm.Name] = cm.Data
			}
			if ac.SecretRef != nil {
				secretObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: ac.SecretRef.Name}
				var secret = &corev1.Secret{}
				if err := r.Get(ctx, secretObjectKey, secret); err != nil {
					return nil, err
				}

				// Convert []byte to string for the hash map
				strData := make(map[string]string, len(secret.Data))
				for k, v := range secret.Data {
					strData[k] = string(v)
				}
				additionalConfData[secret.Name] = strData
			}
		}
	}

	type specsHashInput struct {
		StsSpecAnnotations            map[string]string
		PodSpecTemplate               corev1.PodSpec
		TypesenseContainerResources   []byte
		MetricsContainerResources     []byte
		HealthcheckContainerResources []byte
		AdditionalConfigurationData   map[string]map[string]string
	}

	c0, _ := json.Marshal(sts.Spec.Template.Spec.Containers[0].Resources)
	c1, _ := json.Marshal(sts.Spec.Template.Spec.Containers[1].Resources)
	c2, _ := json.Marshal(sts.Spec.Template.Spec.Containers[2].Resources)

	shi := specsHashInput{
		StsSpecAnnotations:            stsTemplate.Annotations,
		PodSpecTemplate:               *podTemplate,
		TypesenseContainerResources:   c0,
		MetricsContainerResources:     c1,
		HealthcheckContainerResources: c2,
		AdditionalConfigurationData:   additionalConfData,
	}

	h, err := hashstructure.Hash(shi, hashstructure.FormatV2, nil)
	if err != nil {
		return nil, err
	}

	dh := fmt.Sprintf("%d", h)
	b16h := fmt.Sprintf("%x", sha256.Sum256([]byte(dh)))
	return &b16h, nil
}
