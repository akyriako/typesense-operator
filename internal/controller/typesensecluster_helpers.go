package controller

import (
	"context"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TypesenseClusterReconciler) patchStatus(
	ctx context.Context,
	ts *tsv1alpha1.TypesenseCluster,
	patcher func(status *tsv1alpha1.TypesenseClusterStatus),
) error {
	patch := client.MergeFrom(ts.DeepCopy())
	patcher(&ts.Status)

	err := r.Status().Patch(ctx, ts, patch)
	if err != nil {
		r.logger.Error(err, "unable to patch typesense cluster status")
		return err
	}

	return nil
}
