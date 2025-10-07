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

const (
	hooksExecutorContainerName = "velero-hooks-exec"
	IncludeInBackupLabelKey    = "ts.opentelekomcloud.com/include-in-backup"
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

	scheduleName := fmt.Sprintf(ClusterBackupSchedule, ts.Name, ts.Namespace)
	scheduleObjectKey := client.ObjectKey{
		Name:      scheduleName,
		Namespace: ts.Spec.Backup.Velero.Namespace,
	}

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
			"matchLabels": toAnyMapString(getIncludeInBackupLabels(ts)),
		}

		hooks := ts.Spec.GetDefaultBackupHook()
		hooksSpec := map[string]any{
			"name":               "typesense-snapshot",
			"includedNamespaces": toAnySlice([]string{ts.Namespace}),
			"labelSelector":      selector,
			"pre": []any{map[string]any{
				"exec": map[string]any{
					"container": hooksExecutorContainerName,
					"command":   toAnySlice(hooks.Pre.CommandOverride),
					"onError":   hooks.Pre.OnErrorPolicy,
					"timeout":   fmt.Sprintf("%dm", hooks.Pre.TimeoutInMinutes),
				},
			}},
		}

		snapshotVolumes := false // FSB
		if ts.Spec.Backup.Method == "csi" {
			snapshotVolumes = true // CSI
		}

		// set VSLs if provided

		var volumeSnapshotLocations []any
		if snapshotVolumes && len(ts.Spec.Backup.Velero.VolumeSnapshotLocations) == 0 {
			return fmt.Errorf("no volume snapshot locations specified. required for csi method")
		}
		volumeSnapshotLocations = toAnySlice(ts.Spec.Backup.Velero.VolumeSnapshotLocations)

		template := map[string]any{
			"includedNamespaces":       toAnySlice([]string{ts.Namespace}),
			"labelSelector":            selector,
			"snapshotVolumes":          snapshotVolumes, // Use either CSI(true) or FSB(false) method.
			"storageLocation":          ts.Spec.Backup.Velero.BackupStorageLocation,
			"volumeSnapshotLocations":  volumeSnapshotLocations,
			"defaultVolumesToFsBackup": false, // Declare explicitly that we do NOT back up all pod volumes by default, only annotated ones.
			"includeClusterResources":  false, // Skip other cluster-scoped resources
			"excludedResources": toAnySlice([]string{ // Skip CRDs as target clusters may differ. Let the operator rebuild the resources.
				"customresourcedefinitions.apiextensions.k8s.io",
				"clusterroles.rbac.authorization.k8s.io",
				"clusterrolebindings.rbac.authorization.k8s.io",
				"storageclasses.storage.k8s.io",
				"volumesnapshotclasses.snapshot.storage.k8s.io",
				"endpoints",
				"replicasets.apps",
				"statefulsets.apps/status",
				"events",
			}),
			"ttl":   fmt.Sprintf("%dh", ts.Spec.Backup.RetentionInDays*24),
			"hooks": map[string]any{"resources": []any{hooksSpec}},
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
