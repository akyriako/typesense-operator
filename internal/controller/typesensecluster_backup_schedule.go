package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TypesenseClusterReconciler) ReconcileBackup(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	r.logger.V(debugLevel).Info("reconciling backup schedule")

	if ts.Spec.Backup == nil {
		return nil
	}

	if deployed, err := r.IsVeleroDeployed(); err != nil || !deployed {
		if ts.Spec.Metrics != nil {
			err := fmt.Errorf("velero api group %s was not found in cluster", veleroApiGroup)
			r.logger.V(debugLevel).Info("reconciling backup schedule skipped: %s", err.Error())
		}
		return nil
	}

	scheduleName := fmt.Sprintf(ClusterBackupSchedule, ts.Name, ts.Namespace, r.getClusterId(ts))
	scheduleObjectKey := client.ObjectKey{
		Name:      scheduleName,
		Namespace: ts.Spec.Backup.Velero.Namespace,
	}

	r.logger.V(debugLevel).Info("reconciling backup schedule", "schedule", scheduleObjectKey.String())

	err := r.ensureBackupSchedule(ctx, ts, scheduleObjectKey)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) ensureBackupSchedule(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, key client.ObjectKey) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "Schedule",
	})
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)

	_, err := ctrl.CreateOrUpdate(ctx, r.Client, obj, func() error {
		labels := map[string]string{
			"ts.opentelekomcloud.com/owner-uid":       r.getClusterId(ts),
			"ts.opentelekomcloud.com/owner-name":      ts.Name,
			"ts.opentelekomcloud.com/owner-namespace": ts.Namespace,
		}
		obj.SetLabels(labels)

		if err := unstructured.SetNestedField(obj.Object, ts.Spec.Backup.Schedule, "spec", "schedule"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(obj.Object, ts.Spec.Backup.Velero.UseOwnerReferencesInBackup, "spec", "useOwnerReferencesInBackup"); err != nil {
			return err
		}

		selector := map[string]any{
			"matchLabels": toAnyMapString(getLabels(ts)),
		}

		hooks := ts.Spec.GetDefaultBackupHook()
		hooksSpec := map[string]any{
			"name":               "typesense-snapshot",
			"includedNamespaces": toAnySlice([]string{ts.Namespace}),
			"labelSelector":      selector,
			"pre": []any{map[string]any{
				"exec": map[string]any{
					"container": "velero-hooks-exec",
					"command":   toAnySlice(hooks.Pre.CommandOverride),
					"onError":   hooks.Pre.OnErrorPolicy,
					"timeout":   fmt.Sprintf("%dm", hooks.Pre.Timeout),
				},
			}},
		}

		template := map[string]any{
			"includedNamespaces": toAnySlice([]string{ts.Namespace}),
			"labelSelector":      selector,
			"ttl":                fmt.Sprintf("%dh", ts.Spec.Backup.Retention*24),
			"hooks":              map[string]any{"resources": []any{hooksSpec}},
		}

		return unstructured.SetNestedMap(obj.Object, template, "spec", "template")
	})

	return err
}

func (r *TypesenseClusterReconciler) IsVeleroDeployed() (bool, error) {
	apiGroupList, err := r.DiscoveryClient.ServerGroups()
	if err != nil {
		return false, err
	}

	for _, apiGroup := range apiGroupList.Groups {
		if apiGroup.Name == veleroApiGroup {
			return true, nil
		}
	}

	return false, nil
}

func (r *TypesenseClusterReconciler) getClusterId(ts *tsv1alpha1.TypesenseCluster) string {
	uid := string(ts.UID)
	if len(uid) > 8 {
		uid = uid[:8]
	}

	return uid
}
