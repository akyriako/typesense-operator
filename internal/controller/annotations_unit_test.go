package controller

import (
	"context"
	"testing"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	istioInjectKey   = "sidecar.istio.io/inject"
	istioInjectValue = "true"
	istioStatusKey   = "sidecar.istio.io/status"
	istioStatusValue = "{injected}"
	teamKey          = "team"
	teamValue        = "search-platform"
)

// The inject annotation is set by the operator and matches the filter, the status annotation is injected
// externally, and the team annotation is unrelated.
func TestWithoutExternalKeysKeepsManagedKeys(t *testing.T) {
	current := map[string]string{
		istioInjectKey: istioInjectValue,
		istioStatusKey: istioStatusValue,
		teamKey:        teamValue,
	}
	desired := map[string]string{
		istioInjectKey: istioInjectValue,
		teamKey:        teamValue,
	}

	got := withoutExternalKeys(current, desired, "sidecar.istio.io")
	if len(got) != 2 || got[istioInjectKey] != istioInjectValue || got[teamKey] != teamValue {
		t.Errorf("unexpected result %v", got)
	}
}

func TestPreserveExternalKeysPrefersDesired(t *testing.T) {
	current := map[string]string{
		istioInjectKey: "false",
		istioStatusKey: istioStatusValue,
		"stale":        "value",
	}

	got := preserveExternalKeys(map[string]string{istioInjectKey: istioInjectValue}, current, "sidecar.istio.io")
	if got[istioInjectKey] != istioInjectValue {
		t.Errorf("expected desired value to win, got %q", got[istioInjectKey])
	}
	if got[istioStatusKey] != istioStatusValue {
		t.Errorf("expected external annotation to be preserved, got %v", got)
	}
	if _, ok := got["stale"]; ok {
		t.Errorf("expected unfiltered stale annotation to be dropped, got %v", got)
	}

	if got := preserveExternalKeys(nil, current, "sidecar.istio.io"); len(got) != 2 {
		t.Errorf("expected nil desired to be allocated, got %v", got)
	}
}

// A pod annotation that also matches an ignore filter must not make the StatefulSet look outdated,
// otherwise every reconcile stamps restartedAt and restarts the pods.
func TestShouldUpdateStatefulSetIgnoresManagedAnnotationsMatchingFilters(t *testing.T) {
	ts := &tsv1alpha1.TypesenseCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: "a"},
		Spec: tsv1alpha1.TypesenseClusterSpec{
			Replicas:                               3,
			ApiPort:                                8108,
			PeeringPort:                            8107,
			PodAnnotations:                         map[string]string{istioInjectKey: istioInjectValue},
			IgnoreAnnotationsFromExternalMutations: []string{"sidecar.istio.io"},
		},
		Status: tsv1alpha1.TypesenseClusterStatus{
			Conditions: []metav1.Condition{{Type: ConditionTypeReady, Status: metav1.ConditionTrue, Reason: string(ConditionReasonQuorumReady)}},
		},
	}

	r := &TypesenseClusterReconciler{}
	key := client.ObjectKey{Namespace: ts.Namespace, Name: "foo-sts"}
	desired, err := r.buildStatefulSet(context.Background(), key, ts)
	if err != nil {
		t.Fatalf("building statefulset failed: %v", err)
	}

	// Live object after a previous update: restartedAt stamped and a sidecar status injected externally.
	current := desired.DeepCopy()
	current.Spec.Template.Annotations[restartPodsAnnotationKey] = "2026-10-13T10:00:00Z"
	current.Spec.Template.Annotations[istioStatusKey] = istioStatusValue

	update, _, triggers := r.shouldUpdateStatefulSet(current, desired, ts)
	if update {
		t.Errorf("expected no update, got triggers %v", triggers)
	}

	// A change of the operator-managed annotation must still be detected.
	current.Spec.Template.Annotations[istioInjectKey] = "false"
	if update, _, _ := r.shouldUpdateStatefulSet(current, desired, ts); !update {
		t.Error("expected an update when a managed annotation changed")
	}
}

func TestMapPersistentVolumeClaimToTypesenseCluster(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:      "data-foo-sts-0",
		Namespace: "a",
		Labels: map[string]string{
			labelManagedBy: labelManagedByValue,
			labelInstance:  testClusterName,
		},
	}}

	requests := mapPersistentVolumeClaimToTypesenseCluster(context.Background(), pvc)
	if len(requests) != 1 || requests[0].Namespace != "a" || requests[0].Name != testClusterName {
		t.Errorf("unexpected requests %v", requests)
	}

	pvc.Labels[labelManagedBy] = "someone-else"
	if requests := mapPersistentVolumeClaimToTypesenseCluster(context.Background(), pvc); len(requests) != 0 {
		t.Errorf("expected foreign PVC to be ignored, got %v", requests)
	}
}
