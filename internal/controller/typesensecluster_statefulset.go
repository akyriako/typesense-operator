package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
)

func (r *TypesenseClusterReconciler) ReconcileStatefulSet(
	ctx context.Context,
	ts tsv1alpha1.TypesenseCluster,
) (*appsv1.StatefulSet, error) {
	r.logger.V(debugLevel).Info("reconciling statefulset")

	stsName := fmt.Sprintf(ClusterStatefulSet, ts.Name)
	stsExists := true
	stsObjectKey := client.ObjectKey{
		Name:      stsName,
		Namespace: ts.Namespace,
	}

	var sts = &appsv1.StatefulSet{}
	if err := r.Get(ctx, stsObjectKey, sts); err != nil {
		if apierrors.IsNotFound(err) {
			stsExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch statefulset: %s", stsName))
		}
	}

	if !stsExists {
		r.logger.V(debugLevel).Info("creating statefulset", "sts", stsObjectKey)

		sts, err := r.createStatefulSet(
			ctx,
			stsObjectKey,
			&ts,
		)
		if err != nil {
			r.logger.Error(err, "creating statefulset failed", "sts", stsObjectKey)
			return nil, err
		}
		return sts, nil
	}
	return sts, nil
}

func (r *TypesenseClusterReconciler) createStatefulSet(
	ctx context.Context,
	key client.ObjectKey,
	ts *tsv1alpha1.TypesenseCluster,
) (*appsv1.StatefulSet, error) {

	envFrom := ts.Spec.GetAdditionalServerConfiguration()

	sts := &appsv1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: appsv1.StatefulSetSpec{
			ServiceName:         fmt.Sprintf(ClusterHeadlessService, ts.Name),
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Replicas:            ptr.To[int32](ts.Spec.Replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: getLabels(ts),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: getObjectMeta(ts, &key.Name, nil),
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    ptr.To[int64](10000),
						FSGroup:      ptr.To[int64](2000),
						RunAsGroup:   ptr.To[int64](3000),
						RunAsNonRoot: ptr.To[bool](true)},
					TerminationGracePeriodSeconds: ptr.To[int64](5),
					Containers: []corev1.Container{
						{
							Name:            "typesense",
							Image:           ts.Spec.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: int32(ts.Spec.ApiPort),
								},
							},
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
									Name:  "TYPESENSE_NODES",
									Value: "/usr/share/typesense/nodes",
								},
								{
									Name:  "TYPESENSE_DATA_DIR",
									Value: "/usr/share/typesense/data",
								},
								{
									Name:  "TYPESENSE_API_PORT",
									Value: strconv.Itoa(ts.Spec.ApiPort),
								},
								{
									Name:  "TYPESENSE_PEERING_PORT",
									Value: strconv.Itoa(ts.Spec.PeeringPort),
								},
								{
									Name: "TYPESENSE_PEERING_ADDRESS",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										}},
								},
								{
									Name:  "TYPESENSE_ENABLE_CORS",
									Value: strconv.FormatBool(ts.Spec.EnableCors),
								},
								{
									Name:  "TYPESENSE_CORS_DOMAINS",
									Value: ts.Spec.GetCorsDomains(),
								},
								{
									Name:  "TYPESENSE_RESET_PEERS_ON_ERROR",
									Value: strconv.FormatBool(ts.Spec.ResetPeersOnError),
								},
							},
							EnvFrom:   envFrom,
							Resources: ts.Spec.GetResources(),
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/usr/share/typesense",
									Name:      "nodeslist",
								},
								{
									MountPath: "/usr/share/typesense/data",
									Name:      "data",
								},
							},
						},
					},
					NodeSelector: ts.Spec.NodeSelector,
					Tolerations:  ts.Spec.Tolerations,
					Volumes: []corev1.Volume{
						{
							Name: "nodeslist",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf(ClusterNodesConfigMap, ts.Name),
									},
								},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "data",
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "data",
						Labels: getLabels(ts),
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: ts.Spec.GetStorage().Size,
							},
						},
						StorageClassName: &ts.Spec.Storage.StorageClassName,
					},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, sts, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, sts)
	if err != nil {
		return nil, err
	}

	return sts, nil
}

func (r *TypesenseClusterReconciler) ScaleStatefulSet(ctx context.Context, sts *appsv1.StatefulSet, desiredReplicas int32) error {
	if sts.Spec.Replicas != nil && *sts.Spec.Replicas == desiredReplicas {
		r.logger.V(debugLevel).Info("statefulset already scaled to desired replicas", "name", sts.Name, "replicas", desiredReplicas)
		return nil
	}

	desired := sts.DeepCopy()
	desired.Spec.Replicas = &desiredReplicas
	if err := r.Client.Update(ctx, desired); err != nil {
		r.logger.Error(err, "updating stateful replicas failed", "name", desired.Name)
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) PurgeStatefulSetPods(ctx context.Context, sts *appsv1.StatefulSet) error {
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		r.logger.Error(err, "failed to list pods", "statefulset", sts.Name)
		return err
	}

	for _, pod := range pods.Items {
		err := r.Delete(ctx, &pod)
		if err != nil {
			r.logger.Error(err, "failed to delete pod", "pod", pod.Name)
			return err
		}
	}

	return nil
}
