package controller

import (
	"context"
	"fmt"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ForceConfigMapUpdateAnnotation = "ts.opentelekomcloud.com/forced-configmap-update-time"
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

// ForcePodsConfigMapUpdate forces a configmap update for all pods in the statefulset
// it should be called after a configmap update occurs
// https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/#mounted-configmaps-are-updated-automatically
func (r *TypesenseClusterReconciler) ForcePodsConfigMapUpdate(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	labelMap := make(map[string]string)
	labelMap["app"] = fmt.Sprintf(ClusterAppLabel, ts.Name)
	labelSelector := labels.SelectorFromSet(labelMap)

	var podList v1.PodList
	if err := r.Client.List(ctx, &podList,
		client.InNamespace(ts.Namespace),
		client.MatchingLabelsSelector{Selector: labelSelector},
	); err != nil {
		return err
	}

	var err error
	for _, pod := range podList.Items {
		pod.Annotations[ForceConfigMapUpdateAnnotation] = time.Now().Format(time.RFC3339)
		err = r.Update(ctx, &pod)
		if err != nil {
			r.logger.Error(err, "failed to update pod metadata", "pod", pod.Name)
		}
	}
	if err != nil {
		return err
	}
	return nil
}
