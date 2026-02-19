package controller

import (
	"context"
	"fmt"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/version"
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

func (r *TypesenseClusterReconciler) IsFeatureSupported(minimum string) (bool, string, error) {
	info, err := r.DiscoveryClient.ServerVersion()
	if err != nil {
		return false, "", err
	}

	ver, err := version.ParseGeneric(info.GitVersion)
	if err != nil {
		return false, info.GitVersion, err
	}

	req, err := version.ParseGeneric(fmt.Sprintf("v%s", minimum))
	if err != nil {
		return false, info.GitVersion, err
	}

	return ver.AtLeast(req), info.GitVersion, nil
}
