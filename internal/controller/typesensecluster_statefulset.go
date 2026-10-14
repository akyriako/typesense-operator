package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	metricsPort                = 9100
	hashAnnotationKey          = "ts.opentelekomcloud.com/pod-template-hash"
	readLagAnnotationKey       = "ts.opentelekomcloud.com/read-lag-threshold"
	writeLagAnnotationKey      = "ts.opentelekomcloud.com/write-lag-threshold"
	restartPodsAnnotationKey   = "kubectl.kubernetes.io/restartedAt"
	rancherDomainAnnotationKey = "cattle.io"

	// terminationGracePeriodSeconds gives Typesense time to shut down gracefully on SIGTERM (stepping down
	// as Raft leader and flushing its state) before it is killed.
	terminationGracePeriodSeconds int64 = 30

	// singleNodeStartupGracePeriod is how long the Typesense container of a single node cluster may run
	// without answering before the node is considered stuck and its pod is purged.
	singleNodeStartupGracePeriod = 5 * time.Minute
)

func (r *TypesenseClusterReconciler) ReconcileStatefulSet(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (*appsv1.StatefulSet, bool, error) {
	log.FromContext(ctx).V(debugLevel).Info("reconciling statefulset")

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
			log.FromContext(ctx).Error(err, fmt.Sprintf("unable to fetch statefulset: %s", stsName))
			return nil, false, err
		}
	}

	if !stsExists {
		log.FromContext(ctx).V(debugLevel).Info("creating statefulset", "sts", stsObjectKey.Name)

		sts, err := r.createStatefulSet(
			ctx,
			stsObjectKey,
			ts,
		)
		if err != nil {
			log.FromContext(ctx).Error(err, "creating statefulset failed", "sts", stsObjectKey.Name)
			return nil, false, err
		}

		r.logLagThresholds(ctx, sts)
		return sts, false, nil
	} else {
		err := r.expandPersistentVolumeClaims(ctx, sts, ts)
		if err != nil {
			log.FromContext(ctx).Error(err, "expanding pvcs failed", "sts", stsObjectKey.Name)
		}

		skipConditions := []string{
			string(ConditionReasonQuorumDowngraded),
			string(ConditionReasonQuorumUpgraded),
			string(ConditionReasonQuorumNeedsAttentionMemoryOrDiskIssue),
			// string(ConditionReasonQuorumNeedsAttentionClusterIsLagging),
			string(ConditionReasonQuorumNotReady),
			ConditionReasonStatefulSetNotReady,
			ConditionReasonReconciliationInProgress,
			string(ConditionReasonQuorumNotReadyWaitATerm),
		}

		condition := r.getConditionReady(ts)

		if condition != nil {
			emergencyUpdateRequired := r.shouldEmergencyUpdateStatefulSet(sts, ts)
			if !contains(skipConditions, condition.Reason) || emergencyUpdateRequired {
				desiredSts, err := r.buildStatefulSet(ctx, stsObjectKey, ts)
				if err != nil {
					log.FromContext(ctx).Error(err, "building statefulset failed", "sts", stsObjectKey.Name)
					return nil, false, err
				}

				// Prevent scaling operations fighting quorum recovery state
				isScaleDown := ts.Spec.Replicas < ptr.Deref(sts.Spec.Replicas, 1)
				if isQuorumRecoveryState(condition.Reason) && !isScaleDown {
					desiredSts.Spec.Replicas = sts.Spec.Replicas
				}

				update, scaleOnly, triggers := r.shouldUpdateStatefulSet(sts, desiredSts, ts)
				if update {
					oldImage := "unknown"
					if len(sts.Spec.Template.Spec.Containers) > 0 {
						oldImage = getImageTag(sts.Spec.Template.Spec.Containers[0].Image)
					}
					newImage := getImageTag(desiredSts.Spec.Template.Spec.Containers[0].Image)
					if oldImage != newImage && oldImage != "unknown" {
						triggers = append(triggers, SpecTypesenseVersionChanged)
						r.Recorder.Eventf(ts, "Normal", "TypesenseVersionUpdate", "Scheduled update from %s to %s", oldImage, newImage)
					}

					log.FromContext(ctx).V(debugLevel).Info("updating statefulset", "sts", sts.Name, "triggers", triggers)

					updatedSts, err := r.updateStatefulSet(ctx, sts, desiredSts, ts)
					if err != nil {
						log.FromContext(ctx).Error(err, "updating statefulset failed", "sts", stsObjectKey.Name)
						return nil, false, err
					}

					if err := r.syncConfigMapWithStatefulSet(ctx, ts, updatedSts.Spec.Replicas); err != nil {
						log.FromContext(ctx).Error(err, "unable to update config map", "configmap", fmt.Sprintf(ClusterNodesConfigMap, ts.Name))
					}

					r.logLagThresholds(ctx, updatedSts)
					return updatedSts, true, nil
				} else if scaleOnly {
					log.FromContext(ctx).V(debugLevel).Info("scaling statefulset", "sts", sts.Name, "triggers", triggers)

					size := ts.Spec.Replicas
					err = r.ScaleStatefulSet(ctx, stsObjectKey, size)
					if err != nil {
						return desiredSts, true, err
					}

					if err := r.syncConfigMapWithStatefulSet(ctx, ts, &size); err != nil {
						return desiredSts, true, err
					}

					r.logLagThresholds(ctx, desiredSts)
					return desiredSts, true, nil
				}
			}
		}
	}

	r.logLagThresholds(ctx, sts)
	return sts, false, nil
}

// syncConfigMapWithStatefulSet rewrites the nodes config map for the given replica count after the
// StatefulSet has been updated or scaled.
func (r *TypesenseClusterReconciler) syncConfigMapWithStatefulSet(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, replicas *int32) error {
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: fmt.Sprintf(ClusterNodesConfigMap, ts.Name)}

	// Without the live config map there is nothing to patch; updateConfigMap would otherwise apply an
	// object without a name.
	var cm = &corev1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		return err
	}

	updated, err := r.updateConfigMap(ctx, ts, cm, replicas, true)
	if err != nil {
		return err
	}

	if updated && ts.Spec.ForceResetPeersConfigOnUpdate {
		_ = r.forcePodsConfigMapUpdate(ctx, ts)
	}

	return nil
}

func (r *TypesenseClusterReconciler) logLagThresholds(ctx context.Context, sts *appsv1.StatefulSet) {
	read := sts.Spec.Template.Annotations[readLagAnnotationKey]
	write := sts.Spec.Template.Annotations[writeLagAnnotationKey]

	if read == "" {
		read = strconv.Itoa(HealthyReadLagDefaultValue)
	}

	if write == "" {
		write = strconv.Itoa(HealthyWriteLagDefaultValue)
	}

	log.FromContext(ctx).V(debugLevel).Info("reporting lag thresholds", "read", read, "write", write)
}

func (r *TypesenseClusterReconciler) createStatefulSet(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*appsv1.StatefulSet, error) {
	sts, err := r.buildStatefulSet(ctx, key, ts)
	if err != nil {
		return nil, err
	}

	err = ctrl.SetControllerReference(ts, sts, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, sts)
	if err != nil {
		return nil, err
	}

	return sts, nil
}

func (r *TypesenseClusterReconciler) expandPersistentVolumeClaims(ctx context.Context, sts *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) error {
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)
	var pvcs corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &pvcs, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		return err
	}

	desiredSize := ts.Spec.GetStorage().Size

	var errs []error
	for _, pvc := range pvcs.Items {
		currentSize := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if currentSize.Cmp(desiredSize) < 0 {
			patch := client.MergeFrom(pvc.DeepCopy())
			if pvc.Spec.Resources.Requests == nil {
				pvc.Spec.Resources.Requests = corev1.ResourceList{}
			}
			pvc.Spec.Resources.Requests[corev1.ResourceStorage] = desiredSize
			if err := r.Patch(ctx, &pvc, patch); err != nil {
				errs = append(errs, err)
			} else {
				log.FromContext(ctx).Info("expanded pvc", "pvc", pvc.Name, "newSize", desiredSize.String())
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *TypesenseClusterReconciler) updateStatefulSet(ctx context.Context, sts *appsv1.StatefulSet, desired *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) (*appsv1.StatefulSet, error) {
	patch := client.MergeFrom(sts.DeepCopy())

	existingVolumeClaimTemplates := sts.Spec.VolumeClaimTemplates
	existingStsAnnotations := sts.Annotations
	existingPodAnnotations := sts.Spec.Template.Annotations

	sts.Spec = desired.Spec
	sts.Spec.VolumeClaimTemplates = existingVolumeClaimTemplates

	// Preserve external StatefulSet annotations; values the operator sets itself take precedence
	filters := append([]string{rancherDomainAnnotationKey}, ts.Spec.IgnoreAnnotationsFromExternalMutations...)
	sts.Annotations = preserveExternalKeys(desired.Annotations, existingStsAnnotations, filters...)

	// Preserve external Pod annotations; values the operator sets itself take precedence
	podFilters := append([]string{restartPodsAnnotationKey, rancherDomainAnnotationKey}, ts.Spec.IgnoreAnnotationsFromExternalMutations...)
	podAnnotations := preserveExternalKeys(desired.Spec.Template.Annotations, existingPodAnnotations, podFilters...)
	podAnnotations[restartPodsAnnotationKey] = time.Now().Format(time.RFC3339)
	podAnnotations[hashAnnotationKey] = desired.Spec.Template.Annotations[hashAnnotationKey]
	sts.Spec.Template.Annotations = podAnnotations

	if err := r.Patch(ctx, sts, patch); err != nil {
		return nil, err
	}

	return sts, nil
}

func (r *TypesenseClusterReconciler) buildStatefulSet(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*appsv1.StatefulSet, error) {
	readLagThreshold, writeLagThreshold := r.getHealthyLagThresholds(ctx, ts)

	podAnnotations := make(map[string]string)
	podAnnotations[readLagAnnotationKey] = strconv.Itoa(readLagThreshold)
	podAnnotations[writeLagAnnotationKey] = strconv.Itoa(writeLagThreshold)
	if ts.Spec.PodAnnotations != nil {
		for k, v := range ts.Spec.PodAnnotations {
			podAnnotations[k] = v
		}
	}

	stsAnnotations := make(map[string]string)
	if ts.Spec.StatefulSetAnnotations != nil {
		for k, v := range ts.Spec.StatefulSetAnnotations {
			stsAnnotations[k] = v
			if ts.Spec.PodsInheritStatefulSetAnnotations {
				podAnnotations[k] = v
			}
		}
	}

	clusterName := ts.Name

	storageSpec := ts.Spec.GetStorage()
	var storageClassName *string
	if storageSpec.StorageClassName != "" {
		storageClassName = &storageSpec.StorageClassName
	}

	sts := &appsv1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: getObjectMeta(ts, &key.Name, stsAnnotations),
		Spec: appsv1.StatefulSetSpec{
			ServiceName:         fmt.Sprintf(ClusterHeadlessService, clusterName),
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Replicas:            ptr.To[int32](ts.Spec.Replicas),
			PersistentVolumeClaimRetentionPolicy: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
				WhenScaled:  appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: getLabels(ts),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: getObjectMeta(ts, &key.Name, podAnnotations),
				Spec: corev1.PodSpec{
					SecurityContext:               ts.Spec.GetPodSecurityContext(),
					TerminationGracePeriodSeconds: ptr.To(terminationGracePeriodSeconds),
					ReadinessGates: []corev1.PodReadinessGate{
						{
							ConditionType: QuorumReadinessGateCondition,
						},
					},
					PriorityClassName: ptr.Deref[string](ts.Spec.PriorityClassName, ""),
					ImagePullSecrets:  ts.Spec.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            containerTypesense,
							Image:           ts.Spec.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: ts.Spec.GetTypesenseSecurityContext(),
							Ports: []corev1.ContainerPort{
								{
									Name:          protocolHttp,
									ContainerPort: int32(ts.Spec.ApiPort),
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: envTypesenseApiKey,
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
									Value: "/etc/typesense/nodes",
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
							EnvFrom:   ts.Spec.GetAdditionalServerConfiguration(),
							Resources: ts.Spec.GetResources(),
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/etc/typesense",
									Name:      "nodeslist",
								},
								{
									MountPath: "/usr/share/typesense/data",
									Name:      volumeData,
								},
							},
						},
						{
							Name:            "metrics-exporter",
							Image:           ts.Spec.GetMetricsExporterSpecs().Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: ts.Spec.GetMetricsSecurityContext(),
							Ports: []corev1.ContainerPort{
								{
									Name:          "metrics",
									ContainerPort: metricsPort,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: envTypesenseApiKey,
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
									Name:  "LOG_LEVEL",
									Value: strconv.Itoa(ts.Spec.GetMetricsExporterSpecs().LogLevel),
								},
								{
									Name:  envTypesenseProtocol,
									Value: protocolHttp,
								},
								{
									Name:  envTypesenseHost,
									Value: "localhost",
								},
								{
									Name:  envTypesensePort,
									Value: strconv.Itoa(ts.Spec.ApiPort),
								},
								{
									Name:  "METRICS_PORT",
									Value: strconv.Itoa(metricsPort),
								},
								{
									Name:  "TYPESENSE_CLUSTER",
									Value: ts.Name,
								},
							},
							Resources: ts.Spec.GetMetricsExporterResources(),
						},
						{
							Name:            portHealthcheck,
							Image:           ts.Spec.GetHealthCheckSidecarSpecs().Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: ts.Spec.GetHealthcheckSecurityContext(),
							Ports: []corev1.ContainerPort{
								{
									Name:          "healthcheck",
									ContainerPort: 8808,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: envTypesenseApiKey,
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
									Name:  "LOG_LEVEL",
									Value: strconv.Itoa(ts.Spec.GetHealthCheckSidecarSpecs().LogLevel),
								},
								{
									Name:  envTypesenseProtocol,
									Value: protocolHttp,
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
									Name:  "HEALTHCHECK_PORT",
									Value: strconv.Itoa(8808),
								},
								{
									Name:  "TYPESENSE_NODES",
									Value: "/etc/typesense/fallback",
								},
								{
									Name:  "CLUSTER_NAMESPACE",
									Value: ts.Namespace,
								},
							},
							Resources: ts.Spec.GetHealthCheckSidecarResources(),
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/etc/typesense",
									Name:      "nodeslist",
									ReadOnly:  true,
								},
							},
						},
					},
					Affinity:                  ts.Spec.Affinity,
					NodeSelector:              ts.Spec.NodeSelector,
					Tolerations:               ts.Spec.Tolerations,
					TopologySpreadConstraints: ts.Spec.GetTopologySpreadConstraints(getLabels(ts)),
					Volumes: []corev1.Volume{
						{
							Name: volumeNodeslist,
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf(ClusterNodesConfigMap, clusterName),
									},
								},
							},
						},
						{
							Name: volumeData,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: volumeData,
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        volumeData,
						Labels:      getMergedLabels(getDefaultLabels(ts), getLabels(ts)),
						Annotations: storageSpec.Annotations,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.PersistentVolumeAccessMode(storageSpec.AccessMode),
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: storageSpec.Size,
							},
						},
						StorageClassName: storageClassName,
					},
				},
			},
		},
	}

	base16Hash, err := r.buildStatefulSetHash(ctx, sts, ts)
	if err != nil {
		return nil, err
	}

	log.FromContext(ctx).V(debugLevel).Info("calculated hash", "hash", base16Hash)

	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = map[string]string{}
	}
	sts.Spec.Template.Annotations[hashAnnotationKey] = *base16Hash

	return sts, nil
}

func (r *TypesenseClusterReconciler) ScaleStatefulSet(ctx context.Context, stsObjectKey client.ObjectKey, desiredReplicas int32) error {
	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return err
	}

	if ptr.Deref(sts.Spec.Replicas, 1) == desiredReplicas {
		log.FromContext(ctx).V(debugLevel).Info("statefulset already scaled to desired replicas", "name", sts.Name, "replicas", desiredReplicas)
		return nil
	}

	patch := client.MergeFrom(sts.DeepCopy())
	sts.Spec.Replicas = &desiredReplicas
	if err := r.Patch(ctx, sts, patch); err != nil {
		log.FromContext(ctx).Error(err, "updating stateful replicas failed", "name", sts.Name)
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) PurgeStatefulSetPods(ctx context.Context, sts *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) error {
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list pods", "statefulset", sts.Name)
		return err
	}

	var errs []error
	for i := range pods.Items {
		pod := &pods.Items[i]

		pendingPVC, err := r.isPodPendingPersistentVolumeClaim(ctx, pod)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to inspect pod PVCs before purging statefulset pod", "pod", pod.Name)
			return err
		}
		if pendingPVC {
			log.FromContext(ctx).V(debugLevel).Info("skipping purge of pod because PVC is still pending", "pod", pod.Name)
			continue
		}

		err = r.Delete(ctx, pod)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to delete pod", "pod", pod.Name)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
	}

	r.Recorder.Event(ts, "Warning", string(ConditionReasonQuorumPurged), toTitle("quorum has been purged"))

	return nil
}

func (r *TypesenseClusterReconciler) GetUnscheduledPods(ctx context.Context, sts *appsv1.StatefulSet) ([]*corev1.Pod, error) {
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		log.FromContext(ctx).Error(err, "retrieving unscheduled pods: failed to list pods", "statefulset", sts.Name)
		return nil, err
	}

	unscheduledPods := make([]*corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if isPodUnschedulable(pod) {
			unscheduledPods = append(unscheduledPods, pod)
		}
	}

	return unscheduledPods, nil
}

func (r *TypesenseClusterReconciler) RestartUnscheduledPods(ctx context.Context, pods []*corev1.Pod, ts *tsv1alpha1.TypesenseCluster) error {
	removedAny := false
	for _, pod := range pods {
		if !isPodUnschedulable(pod) {
			continue
		}

		pendingPVC, err := r.isPodPendingPersistentVolumeClaim(ctx, pod)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to inspect pod PVCs before restarting unscheduled pod", "pod", pod.Name)
			continue
		}
		if pendingPVC {
			log.FromContext(ctx).V(debugLevel).Info("skipping restart of unscheduled pod because PVC is still pending", "pod", pod.Name)
			continue
		}

		log.FromContext(ctx).V(debugLevel).Info("removing unscheduled pod", "pod", pod.Name)

		propagation := metav1.DeletePropagationBackground
		err = r.Delete(ctx, pod, &client.DeleteOptions{PropagationPolicy: &propagation})
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to remove unscheduled pod", "pod", pod.Name)
		}

		if !removedAny {
			removedAny = err == nil
		}
	}

	if removedAny {
		r.Recorder.Event(ts, "Warning", ConditionReasonStatefulSetNotReady, toTitle("removed unscheduled pods"))
	}

	return nil
}

func (r *TypesenseClusterReconciler) isPodPendingPersistentVolumeClaim(ctx context.Context, pod *corev1.Pod) (bool, error) {
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}

		pvc := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: volume.PersistentVolumeClaim.ClaimName}, pvc); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err
		}

		if pvc.Status.Phase != corev1.ClaimBound {
			return true, nil
		}
	}

	return false, nil
}

func (r *TypesenseClusterReconciler) GetFreshStatefulSet(ctx context.Context, stsObjectKey client.ObjectKey) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, stsObjectKey, sts); err != nil {
		if !apierrors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, fmt.Sprintf("unable to fetch statefulset: %s", stsObjectKey.Name))
		}
		return nil, err
	}

	return sts, nil
}
