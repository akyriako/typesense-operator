package controller

import (
	"context"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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

func (r *TypesenseClusterReconciler) ensureLabels(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	scheduleName := getIncludeInBackupLabels(ts)[IncludeInBackupLabelKey]

	// 1) Ensure label on the TypesenseCluster itself
	if lbls := ts.GetLabels(); lbls == nil || lbls[IncludeInBackupLabelKey] != scheduleName {
		before := ts.DeepCopy()
		if lbls == nil {
			lbls = map[string]string{}
		}
		lbls[IncludeInBackupLabelKey] = scheduleName
		ts.SetLabels(lbls)

		if err := r.Patch(ctx, ts, client.MergeFrom(before)); err != nil {
			r.logger.Error(err, "failed to patch app label on TypesenseCluster")
			return err
		}
	}

	// 2) If an additional server configuration is referenced, ensure the ConfigMap has the same app label
	if ts.Spec.AdditionalServerConfiguration != nil {
		cmName := ts.Spec.AdditionalServerConfiguration.Name
		if cmName != "" {
			var cm corev1.ConfigMap
			if err := r.Get(ctx, client.ObjectKey{Namespace: ts.Namespace, Name: cmName}, &cm); err != nil {
				r.logger.Error(err, "unable to get additional server configuration configmap", "configMap", cmName)
				return err
			}

			if lbls := cm.GetLabels(); lbls == nil || lbls[IncludeInBackupLabelKey] != scheduleName {
				before := cm.DeepCopy()
				if lbls == nil {
					lbls = map[string]string{}
				}
				lbls[IncludeInBackupLabelKey] = scheduleName
				cm.SetLabels(lbls)

				if err := r.Patch(ctx, &cm, client.MergeFrom(before)); err != nil {
					r.logger.Error(err, "failed to patch backup label on configmap", "configMap", cmName)
					return err
				}
			}
		}
	}

	return nil
}
