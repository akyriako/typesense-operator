package controller

import (
	"context"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TypesenseClusterReconciler) ReconcilePodDisruptionBudget(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet) error {
	pdb := &policyv1.PodDisruptionBudget{}
	pdbKey := types.NamespacedName{Name: ts.Name, Namespace: ts.Namespace}

	// PDBs only make sense for highly available clusters.
	// A PDB on a single-replica cluster would indefinitely block node drains.
	if ts.Spec.Replicas <= 1 {
		err := r.Get(ctx, pdbKey, pdb)
		if err == nil {
			err = r.Delete(ctx, pdb)
			return client.IgnoreNotFound(err)
		}
		return client.IgnoreNotFound(err)
	}

	maxUnavailableInt := int((ts.Spec.Replicas - 1) / 2)
	if maxUnavailableInt < 1 && ts.Spec.Replicas > 1 {
		maxUnavailableInt = 1
	}
	maxUnavailable := intstr.FromInt(maxUnavailableInt)

	desired := &policyv1.PodDisruptionBudget{
		TypeMeta: metav1.TypeMeta{
			APIVersion: policyv1.SchemeGroupVersion.String(),
			Kind:       "PodDisruptionBudget",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ts.Name,
			Namespace: ts.Namespace,
			Labels:    getMergedLabels(getDefaultLabels(ts), getLabels(ts)),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector:       sts.Spec.Selector.DeepCopy(),
		},
	}

	if err := ctrl.SetControllerReference(ts, desired, r.Scheme); err != nil {
		return err
	}

	//nolint:staticcheck
	return r.Patch(ctx, desired, client.Apply, client.ForceOwnership, client.FieldOwner("typesense-operator"))
}
