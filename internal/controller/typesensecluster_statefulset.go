package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/mitchellh/hashstructure/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	metricsPort                        = 9100
	startupProbeFailureThreshold int32 = 30
	startupProbePeriodSeconds    int32 = 10
	hashAnnotationKey                  = "ts.opentelekomcloud.com/pod-template-hash"
	readLagAnnotationKey               = "ts.opentelekomcloud.com/read-lag-threshold"
	writeLagAnnotationKey              = "ts.opentelekomcloud.com/write-lag-threshold"
	restartPodsAnnotationKey           = "kubectl.kubernetes.io/restartedAt"
	rancherDomainAnnotationKey         = "cattle.io"
	autopilotAnnotationKey             = "autopilot.gke.io"
	wardenAnnotationKey                = "autopilot.gke.io/warden-version"
)

func (r *TypesenseClusterReconciler) ReconcileStatefulSet(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (*appsv1.StatefulSet, error) {
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
			return nil, err
		}
	}

	if !stsExists {
		r.logger.V(debugLevel).Info("creating statefulset", "sts", stsObjectKey.Name)

		sts, err := r.createStatefulSet(
			ctx,
			stsObjectKey,
			ts,
		)
		if err != nil {
			r.logger.Error(err, "creating statefulset failed", "sts", stsObjectKey.Name)
			return nil, err
		}

		r.logLagThresholds(sts)
		return sts, nil
	} else {
		// Always scale up if below spec replicas, regardless of quorum condition.
		// This prevents the STS from getting stuck at a reduced replica count when
		// the condition is in a skip state (e.g. QuorumNotReadyWaitATerm).
		if sts.Spec.Replicas != nil && *sts.Spec.Replicas < ts.Spec.Replicas {
			r.logger.V(debugLevel).Info("scaling statefulset to match spec replicas",
				"sts", sts.Name, "current", *sts.Spec.Replicas, "desired", ts.Spec.Replicas)
			desiredSts, err := r.buildStatefulSet(ctx, stsObjectKey, ts)
			if err != nil {
				r.logger.Error(err, "building statefulset failed", "sts", stsObjectKey.Name)
			} else {
				updatedSts, err := r.updateStatefulSet(ctx, sts, desiredSts, false)
				if err != nil {
					r.logger.Error(err, "scaling statefulset failed", "sts", stsObjectKey.Name)
					return nil, err
				}
				r.logLagThresholds(updatedSts)
				return updatedSts, nil
			}
		}

		skipConditions := []string{
			string(ConditionReasonQuorumDowngraded),
			string(ConditionReasonQuorumUpgraded),
			string(ConditionReasonQuorumNeedsAttentionMemoryOrDiskIssue),
			//string(ConditionReasonQuorumNeedsAttentionClusterIsLagging),
			string(ConditionReasonQuorumNotReady),
			ConditionReasonStatefulSetNotReady,
			ConditionReasonReconciliationInProgress,
			string(ConditionReasonQuorumNotReadyWaitATerm),
		}

		condition := r.getConditionReady(ts)

		if condition != nil {
			emergencyUpdateRequired := r.shouldEmergencyUpdateStatefulSet(sts, ts)
			if _, contains := contains(skipConditions, condition.Reason); !contains || emergencyUpdateRequired {
				desiredSts, err := r.buildStatefulSet(ctx, stsObjectKey, ts)
				if err != nil {
					r.logger.Error(err, "building statefulset failed", "sts", stsObjectKey.Name)
				}

				update, triggers := r.shouldUpdateStatefulSet(sts, desiredSts, ts)
				if update {
					restartPods := triggersRequireRestart(triggers)
					r.logger.V(debugLevel).Info("updating statefulset", "sts", sts.Name, "triggers", triggers, "restartPods", restartPods)

					oldImage := strings.Replace(sts.Spec.Template.Spec.Containers[0].Image, "typesense/typesense:", "", -1)
					newImage := strings.Replace(desiredSts.Spec.Template.Spec.Containers[0].Image, "typesense/typesense:", "", -1)
					if oldImage != newImage {
						r.logger.V(debugLevel).Info("scheduling typesense update", "current", oldImage, "target", newImage)
						r.Recorder.Eventf(ts, "Normal", "TypesenseVersionUpdate", "Scheduled update from %s to %s", oldImage, newImage)
					}

					updatedSts, err := r.updateStatefulSet(ctx, sts, desiredSts, restartPods)
					if err != nil {
						r.logger.Error(err, "updating statefulset failed", "sts", stsObjectKey.Name)
						return nil, err
					}

					configMapName := fmt.Sprintf(ClusterNodesConfigMap, ts.Name)
					configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

					var cm = &corev1.ConfigMap{}
					if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
						r.logger.V(debugLevel).Error(err, fmt.Sprintf("unable to fetch config map: %s", configMapName))
					}

					_, _, updated, err := r.updateConfigMap(ctx, ts, cm, updatedSts.Spec.Replicas, true)
					if err != nil {
						r.logger.V(debugLevel).Error(err, fmt.Sprintf("unable to update config map: %s", configMapName))
					}

					if updated && ts.Spec.ForceResetPeersConfigOnUpdate {
						_ = r.forcePodsConfigMapUpdate(ctx, ts)
					}

					r.logLagThresholds(updatedSts)
					return updatedSts, nil
				}
			}
		}
	}

	r.logLagThresholds(sts)
	return sts, nil
}

func (r *TypesenseClusterReconciler) logLagThresholds(sts *appsv1.StatefulSet) {
	read := sts.Spec.Template.Annotations[readLagAnnotationKey]
	write := sts.Spec.Template.Annotations[writeLagAnnotationKey]

	if read == "" {
		read = strconv.Itoa(HealthyReadLagDefaultValue)
	}

	if write == "" {
		write = strconv.Itoa(HealthyWriteLagDefaultValue)
	}

	r.logger.V(debugLevel).Info("reporting lag thresholds", "read", read, "write", write)
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

func (r *TypesenseClusterReconciler) updateStatefulSet(ctx context.Context, sts *appsv1.StatefulSet, desired *appsv1.StatefulSet, restartPods bool) (*appsv1.StatefulSet, error) {
	patch := client.MergeFrom(sts.DeepCopy())
	sts.Spec = desired.Spec

	sts.ObjectMeta.Annotations = desired.ObjectMeta.Annotations

	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = map[string]string{}
	}
	if restartPods {
		sts.Spec.Template.Annotations[restartPodsAnnotationKey] = time.Now().Format(time.RFC3339)
	}
	sts.Spec.Template.Annotations[hashAnnotationKey] = desired.Spec.Template.Annotations[hashAnnotationKey]

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
	sts := &appsv1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: getObjectMeta(ts, &key.Name, stsAnnotations),
		Spec: appsv1.StatefulSetSpec{
			ServiceName:         fmt.Sprintf(ClusterHeadlessService, clusterName),
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Replicas:            ptr.To[int32](ts.Spec.Replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: getLabels(ts),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: getObjectMeta(ts, &key.Name, podAnnotations),
				Spec: corev1.PodSpec{
					SecurityContext:               ts.Spec.GetPodSecurityContext(),
					TerminationGracePeriodSeconds: ptr.To[int64](5),
					ReadinessGates: []corev1.PodReadinessGate{
						{
							ConditionType: QuorumReadinessGateCondition,
						},
					},
					PriorityClassName: ptr.Deref[string](ts.Spec.PriorityClassName, ""),
					ImagePullSecrets:  ts.Spec.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            "typesense",
							Image:           ts.Spec.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: ts.Spec.GetTypesenseSecurityContext(),
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
									Value: "/usr/share/typesense/fallback",
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
									MountPath: "/usr/share/typesense",
									Name:      "nodeslist",
								},
								{
									MountPath: "/usr/share/typesense/data",
									Name:      "data",
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
									Name:  "LOG_LEVEL",
									Value: strconv.Itoa(0),
								},
								{
									Name:  "TYPESENSE_PROTOCOL",
									Value: "http",
								},
								{
									Name:  "TYPESENSE_HOST",
									Value: "localhost",
								},
								{
									Name:  "TYPESENSE_PORT",
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
							Name:            "healthcheck",
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
									Name:  "LOG_LEVEL",
									Value: strconv.Itoa(0),
								},
								{
									Name:  "TYPESENSE_PROTOCOL",
									Value: "http",
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
									Value: "/usr/share/typesense/fallback",
								},
								{
									Name:  "CLUSTER_NAMESPACE",
									Value: ts.Namespace,
								},
							},
							Resources: ts.Spec.GetHealthCheckSidecarResources(),
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/usr/share/typesense",
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
							Name: "nodeslist",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf(ClusterNodesConfigMap, clusterName),
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
						Name:        "data",
						Labels:      getLabels(ts),
						Annotations: ts.Spec.GetStorage().Annotations,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.PersistentVolumeAccessMode(ts.Spec.GetStorage().AccessMode),
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

	podTemplateHash, err := hashstructure.Hash(sts.Spec.Template.Spec, hashstructure.FormatV2, nil)
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(sts.Spec.Template.Spec.Containers[0].Resources)
	if err != nil {
		return nil, err
	}
	resourcesHash, err := hashstructure.Hash(
		string(b),
		hashstructure.FormatV2,
		nil,
	)

	specsHash := fmt.Sprintf("%d%d", podTemplateHash, resourcesHash)

	if additionalConfiguration := ts.Spec.GetAdditionalServerConfiguration(); additionalConfiguration != nil {
		for _, ac := range additionalConfiguration {
			if ac.ConfigMapRef != nil {
				configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: ac.ConfigMapRef.Name}
				var cm = &corev1.ConfigMap{}
				if err = r.Get(ctx, configMapObjectKey, cm); err != nil {
					return nil, err
				}

				data := fmt.Sprintf("%v", cm.Data)
				if strings.TrimSpace(data) != "" {
					dataHash, err := hashstructure.Hash(data, hashstructure.FormatV2, nil)
					if err != nil {
						return nil, err
					}

					specsHash = fmt.Sprintf("%s%d", specsHash, dataHash)
				}
			}
		}
	}

	base16Hash := fmt.Sprintf("%x", sha256.Sum256([]byte(specsHash)))
	r.logger.V(debugLevel).Info("calculated hash", "hash", base16Hash)

	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = map[string]string{}
	}
	sts.Spec.Template.Annotations[hashAnnotationKey] = base16Hash

	return sts, nil
}

type UpdateStatefulSetTrigger string

var (
	BelowSpecReplicas               UpdateStatefulSetTrigger = "BelowSpecReplicas"
	HashAnnotationChanged           UpdateStatefulSetTrigger = "HashAnnotationChanged"
	PodAnnotationsChanged           UpdateStatefulSetTrigger = "PodAnnotationsChanged"
	StatefulSetAnnotationsChanged   UpdateStatefulSetTrigger = "StatefulSetAnnotationsChanged"
	SpecResourcesChanged            UpdateStatefulSetTrigger = "SpecResourcesChanged"
	PodSecurityContextChanged       UpdateStatefulSetTrigger = "PodSecurityContextChanged"
	InvalidContainerCount           UpdateStatefulSetTrigger = "InvalidContainerCount"
	ContainerSecurityContextChanged UpdateStatefulSetTrigger = "ContainerSecurityContextChanged"
)

// specLevelTriggers are triggers that indicate a genuine pod template spec change
// which requires restarting pods to take effect.
var specLevelTriggers = map[UpdateStatefulSetTrigger]struct{}{
	BelowSpecReplicas:               {},
	HashAnnotationChanged:           {},
	SpecResourcesChanged:            {},
	PodSecurityContextChanged:       {},
	InvalidContainerCount:           {},
	ContainerSecurityContextChanged: {},
}

// triggersRequireRestart returns true if any of the triggers indicate a spec-level
// change that requires restarting pods.
func triggersRequireRestart(triggers []UpdateStatefulSetTrigger) bool {
	for _, t := range triggers {
		if _, ok := specLevelTriggers[t]; ok {
			return true
		}
	}
	return false
}

func (r *TypesenseClusterReconciler) shouldUpdateStatefulSet(sts *appsv1.StatefulSet, desired *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) (update bool, triggers []UpdateStatefulSetTrigger) {
	update = false

	if sts == nil || ts == nil {
		return false, nil
	}

	condition := r.getConditionReady(ts)
	if condition == nil {
		return false, nil
	}

	// BelowSpecReplicas
	if *sts.Spec.Replicas != ts.Spec.Replicas &&
		(condition.Reason != string(ConditionReasonQuorumDowngraded) || condition.Reason != string(ConditionReasonQuorumQueuedWrites)) {
		triggers = append(triggers, BelowSpecReplicas)
		update = true
	}

	// HashAnnotationChanged
	if sts.Spec.Template.Annotations[hashAnnotationKey] != desired.Spec.Template.Annotations[hashAnnotationKey] {
		triggers = append(triggers, HashAnnotationChanged)
		update = true
	}

	// Filter out annotations injected by external tools (Rancher, GKE Autopilot, operator itself)
	// so that external mutations do not trigger unnecessary StatefulSet updates and pod restarts.
	annotationFilters := []string{
		rancherDomainAnnotationKey,
		autopilotAnnotationKey,
		forceConfigMapUpdateAnnotationKey,
	}
	podAnnotationFilters := append([]string{restartPodsAnnotationKey, hashAnnotationKey}, annotationFilters...)

	stsAnnotations := filterAnnotations(sts.ObjectMeta.Annotations, annotationFilters...)
	podAnnotations := filterAnnotations(sts.Spec.Template.Annotations, podAnnotationFilters...)
	desiredPodAnnotations := filterAnnotations(desired.Spec.Template.Annotations, hashAnnotationKey)

	// PodAnnotationsChanged
	if !apiequality.Semantic.DeepEqual(podAnnotations, desiredPodAnnotations) {
		triggers = append(triggers, PodAnnotationsChanged)
		update = true
	}

	// StatefulSetAnnotationsChanged
	if !apiequality.Semantic.DeepEqual(stsAnnotations, desired.ObjectMeta.Annotations) {
		triggers = append(triggers, StatefulSetAnnotationsChanged)
		update = true
	}

	// NOTE: SpecResourcesChanged, PodSecurityContextChanged, and ContainerSecurityContextChanged
	// are intentionally NOT checked here. These comparisons cause infinite update loops when
	// admission webhooks (GKE Autopilot, Kyverno, Gatekeeper) mutate the StatefulSet by adding
	// fields like ephemeral-storage to resources or seccompProfile to securityContext.
	// The HashAnnotationChanged trigger already covers genuine CR spec changes since the hash
	// is computed from the desired pod template spec built from the CR.

	// InvalidContainerCount
	if len(sts.Spec.Template.Spec.Containers) < 3 {
		triggers = append(triggers, InvalidContainerCount)
		update = true
	}

	return update, triggers
}

func (r *TypesenseClusterReconciler) shouldEmergencyUpdateStatefulSet(sts *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) bool {
	if sts == nil || ts == nil {
		return false
	}

	// NOTE: Resource and SecurityContext comparisons are intentionally removed here.
	// Admission webhooks (GKE Autopilot, Kyverno, Gatekeeper) mutate these fields on the
	// live StatefulSet, causing permanent diffs against the CR spec and infinite update loops.
	// Genuine CR spec changes are detected via HashAnnotationChanged in shouldUpdateStatefulSet.

	// InvalidContainerCount
	if len(sts.Spec.Template.Spec.Containers) < 3 {
		return true
	}

	return false
}
func (r *TypesenseClusterReconciler) ScaleStatefulSet(ctx context.Context, stsObjectKey client.ObjectKey, desiredReplicas int32) error {
	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return err
	}

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

func (r *TypesenseClusterReconciler) PurgeStatefulSetPods(ctx context.Context, sts *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) error {
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

	r.Recorder.Eventf(ts, "Warning", string(ConditionReasonQuorumPurged), toTitle("quorum has been purged"))

	return nil
}

func (r *TypesenseClusterReconciler) GetUnscheduledPods(ctx context.Context, sts *appsv1.StatefulSet) ([]*corev1.Pod, error) {
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		r.logger.Error(err, "retrieving unscheduled pods: failed to list pods", "statefulset", sts.Name)
		return nil, err
	}

	unscheduledPods := make([]*corev1.Pod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodPending {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
					unscheduledPods = append(unscheduledPods, &pod)
				}
			}
		}
	}

	return unscheduledPods, nil
}

func (r *TypesenseClusterReconciler) RestartUnscheduledPods(ctx context.Context, pods []*corev1.Pod, ts *tsv1alpha1.TypesenseCluster) error {
	removedAny := false
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodPending {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
					r.logger.V(debugLevel).Info("removing unscheduled pod", "pod", pod.Name)

					propagation := metav1.DeletePropagationBackground
					err := r.Delete(ctx, pod, &client.DeleteOptions{PropagationPolicy: &propagation})
					if err != nil {
						r.logger.Error(err, "failed to remove unscheduled pod", "pod", pod.Name)
					}

					if !removedAny {
						removedAny = err == nil
					}
				}
			}
		}
	}

	if removedAny {
		r.Recorder.Eventf(ts, "Warning", ConditionReasonStatefulSetNotReady, toTitle("removed unscheduled pods"))
	}

	return nil
}

func (r *TypesenseClusterReconciler) RestartAllUnscheduledPods(ctx context.Context, sts *appsv1.StatefulSet, ts *tsv1alpha1.TypesenseCluster) error {
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)

	var pods corev1.PodList
	if err := r.List(ctx, &pods, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		r.logger.Error(err, "deleting unscheduled pods: failed to list pods", "statefulset", sts.Name)
		return err
	}

	removedAny := false
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodPending {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
					propagation := metav1.DeletePropagationBackground
					err := r.Delete(ctx, &pod, &client.DeleteOptions{
						PropagationPolicy: &propagation,
					})

					if !removedAny {
						removedAny = err == nil
					}
				}
			}
		}
	}

	if removedAny {
		r.Recorder.Eventf(ts, "Warning", ConditionReasonStatefulSetNotReady, toTitle("removed unscheduled pods"))
	}

	return nil
}

func (r *TypesenseClusterReconciler) GetFreshStatefulSet(ctx context.Context, stsObjectKey client.ObjectKey) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, stsObjectKey, sts); err != nil {
		if !apierrors.IsNotFound(err) {
			r.logger.Error(err, fmt.Sprintf("unable to fetch statefulset: %s", stsObjectKey.Name))
		}
		return nil, err
	}

	return sts, nil
}
