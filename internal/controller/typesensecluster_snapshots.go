package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	hooksSidecarContainerName = "velero-hooks-exec"
)

func (r *TypesenseClusterReconciler) ReconcileBackup(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet) error {
	r.logger.V(debugLevel).Info("reconciling backup schedule")

	if ts.Spec.Backup == nil {
		return nil
	}

	if deployed, err := r.IsVeleroDeployed(); err != nil || !deployed {
		if ts.Spec.Metrics != nil {
			err := fmt.Errorf("velero api group %s was not found in cluster", prometheusApiGroup)
			r.logger.Error(err, "reconciling backup schedule skipped")
		}
		return nil
	}

	return r.addHooksSidecar(ctx, ts, sts)
}

func (r *TypesenseClusterReconciler) addHooksSidecar(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet) error {
	r.logger.V(debugLevel).Info("reconciling hooks sidecar")

	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == hooksSidecarContainerName {
			return nil
		}
	}

	desired := sts.DeepCopy()
	sidecar := &corev1.Container{
		Name:            hooksSidecarContainerName,
		Image:           "curlimages/curl:8.8.0",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"sleep", "infinity"},
		Env: []corev1.EnvVar{
			{
				Name: "TYPESENSE_API_KEY",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: ClusterAdminApiKeySecretKeyName,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: r.getAdminApiKeyObjectKey(ts).Name,
						},
					},
				},
			},
			{
				Name:  "TYPESENSE_DATA_DIR",
				Value: "/usr/share/typesense/data",
			},
			{
				Name:  "TYPESENSE_SNAPSHOTS_DIR",
				Value: "/usr/share/typesense/snapshots",
			},
		},
		Resources: ts.Spec.GetHooksSidecarResources(),
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: "/usr/share/typesense/data",
				Name:      "data",
			},
			{
				MountPath: "/usr/share/typesense/snapshots",
				Name:      "snapshots",
			},
		},
	}

	containers := append(sts.Spec.Template.Spec.Containers, *sidecar)
	desired.Spec.Template.Spec.Containers = containers

	volumeExists := false
	for _, v := range sts.Spec.Template.Spec.Volumes {
		if v.Name == "snapshots" {
			volumeExists = true
		}
	}

	if !volumeExists {
		volume := corev1.Volume{
			Name: "snapshots",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "snapshots",
				},
			},
		}

		volumes := append(sts.Spec.Template.Spec.Volumes, volume)
		desired.Spec.Template.Spec.Volumes = volumes
	}

	if err := r.Client.Update(ctx, desired); err != nil {
		r.logger.Error(err, "updating stateful containers failed", "name", desired.Name)
		return err
	}

	return nil
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
