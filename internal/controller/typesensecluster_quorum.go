package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const (
	PodReadinessGateCondition = "RaftQuorumReady"
)

func (r *TypesenseClusterReconciler) ReconcileQuorum(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet, secret *v1.Secret) (ConditionQuorum, int, error) {
	r.logger.Info("reconciling quorum")

	quorum, err := r.getQuorum(ctx, ts, sts)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	r.logger.V(debugLevel).Info("calculating quorum", "MinRequiredNodes", quorum.MinRequiredNodes, "AvailableNodes", quorum.AvailableNodes)

	nodesStatus := make(map[string]NodeStatus)
	httpClient := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	for _, node := range quorum.Nodes {
		status, err := r.getNodeStatus(ctx, httpClient, node, ts, secret)
		if err != nil {
			r.logger.Error(err, "fetching node status failed", "node", node)
			status = NodeStatus{
				State: NotReadyState,
			}
		}

		nodesStatus[node] = status
	}

	clusterStatus := r.getClusterStatus(quorum, nodesStatus)

	if clusterStatus == ClusterStatusSplitBrain {
		return r.downgradeQuorum(ctx, ts, quorum)
	}

	if clusterStatus == ClusterStatusNotReady {
		clusterNeedsAttention := false
		nodesHealth := make(map[string]bool)

		for n, node := range quorum.Nodes {
			nodeStatus := nodesStatus[node]
			conditionReason := "NodeHealthy"
			conditionMessage := fmt.Sprintf("node's role is now: %s", nodeStatus.State)
			conditionStatus := v1.ConditionTrue

			health, err := r.getNodeHealth(ctx, httpClient, node, ts)
			if err != nil {
				conditionReason = "GetNodeHealthFailed"
				conditionStatus = v1.ConditionFalse

				r.logger.Error(err, "fetching node health failed", "node", node)
			} else {
				if !health.Ok {
					if health.ResourceError != nil && (*health.ResourceError == OutOfMemory || *health.ResourceError == OutOfDisk) {
						clusterNeedsAttention = true

						conditionReason = "NodeNotRecoverable"
						conditionMessage = fmt.Sprintf("node is failing: %s", string(*health.ResourceError))
						conditionStatus = v1.ConditionFalse

						err := fmt.Errorf("health check reported a blocking node error on %s: %s", node, string(*health.ResourceError))
						r.logger.Error(err, "quorum cannot be recovered automatically")
					}

					nodesHealth[node] = false
				} else {
					nodesHealth[node] = true
				}
			}

			podName := fmt.Sprintf("%s-%d", fmt.Sprintf(ClusterStatefulSet, ts.Name), n)
			podObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: podName}

			pod := &v1.Pod{}
			err = r.Get(ctx, podObjectKey, pod)
			if err != nil {
				r.logger.Error(err, fmt.Sprintf("unable to fetch statefulset pod: %s", podObjectKey.Name))
				continue
			}

			err = r.updatePodReadinessGate(ctx, pod, conditionStatus, conditionReason, conditionMessage)
			if err != nil {
				r.logger.Error(err, fmt.Sprintf("unable to update statefulset pod: %s", podObjectKey.Name))
				return ConditionReasonQuorumNotReady, 0, err
			}
		}

		// refresh statefulset because it has been updated through the process
		stsObjectKey := client.ObjectKeyFromObject(sts)
		sts = &appsv1.StatefulSet{}
		if err := r.Get(ctx, stsObjectKey, sts); err != nil {
			r.logger.Error(err, fmt.Sprintf("unable to fetch statefulset: %s", stsObjectKey.Name))
			return ConditionReasonQuorumNotReady, 0, err
		}

		if clusterNeedsAttention {
			return ConditionReasonQuorumNeedsAttention, 0, fmt.Errorf("cluster needs administrative attention")
		}

		if sts.Status.ReadyReplicas < int32(quorum.MinRequiredNodes) {
			return r.downgradeQuorum(ctx, ts, quorum)
		}

	}

	//if sts.Status.ReadyReplicas != sts.Status.Replicas && (sts.Status.ReadyReplicas < int32(quorum.MinRequiredNodes) && quorum.MinRequiredNodes > 1) {
	//	return ConditionReasonStatefulSetNotReady, 0, fmt.Errorf("statefulset not ready: %d/%d replicas ready", sts.Status.ReadyReplicas, sts.Status.Replicas)
	//}
	//
	//condition, size, err := r.getQuorumHealth(ctx, &ts, &sts, quorum)
	//r.logger.Info("reconciling quorum completed", "condition", condition)
	//return condition, size, err
	return ConditionReasonQuorumReady, 0, nil
}

func (r *TypesenseClusterReconciler) downgradeQuorum(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, quorum *Quorum) (ConditionQuorum, int, error) {
	if quorum.HealthyNodes == 0 && quorum.MinRequiredNodes == 1 {
		r.logger.Info("purging quorum")
		err := r.PurgeStatefulSetPods(ctx, quorum.StatefulSet)
		if err != nil {
			return ConditionReasonQuorumNotReady, 0, err
		}

		return ConditionReasonQuorumNotReady, 0, nil
	}

	r.logger.Info("downgrading quorum")
	desiredReplicas := int32(1)

	_, size, err := r.updateConfigMap(ctx, ts, quorum.NodesListConfigMap, ptr.To[int32](desiredReplicas))
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	err = r.ScaleStatefulSet(ctx, quorum.StatefulSet, desiredReplicas)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	return ConditionReasonQuorumDowngraded, size, nil
}

func (r *TypesenseClusterReconciler) updatePodReadinessGate(ctx context.Context, pod *v1.Pod, conditionStatus v1.ConditionStatus, conditionReason, conditionMessage string) error {
	patch := client.MergeFrom(pod.DeepCopy())
	condition := v1.PodCondition{
		Type:    PodReadinessGateCondition,
		Status:  conditionStatus,
		Reason:  conditionReason,
		Message: conditionMessage,
	}

	r.logger.V(debugLevel).Info("updating pod readiness gate condition", "pod", pod.Name, "condition", condition)

	updated := false
	for i, c := range pod.Status.Conditions {
		if c.Type == PodReadinessGateCondition {
			pod.Status.Conditions[i] = condition
			updated = true
			break
		}
	}
	if !updated {
		pod.Status.Conditions = append(pod.Status.Conditions, condition)
	}

	if err := r.Status().Patch(ctx, pod, patch); err != nil {
		r.logger.Error(err, "updating pod readiness gate condition failed", "pod", pod.Name)
		return err
	}

	return nil
}

//func (r *TypesenseClusterReconciler) getQuorumHealth(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet, q *Quorum) (ConditionQuorum, int, error) {
//	availableNodes := q.AvailableNodes
//	minRequiredNodes := q.MinRequiredNodes
//	nodes := q.Nodes
//	cm := q.NodesListConfigMap
//
//	if availableNodes < minRequiredNodes {
//		return ConditionReasonQuorumNotReady, availableNodes, fmt.Errorf("quorum has less than minimum %d available Nodes", minRequiredNodes)
//	}
//
//	healthResults := make(map[string]bool, availableNodes)
//	httpClient := &http.Client{
//		Timeout: 500 * time.Millisecond,
//	}
//
//	for _, node := range nodes {
//		ready, err := r.getNodeHealth(httpClient, node, ts)
//		if err != nil {
//			r.logger.Error(err, "health check failed", "node", node, "health", false)
//		} else {
//			r.logger.V(debugLevel).Info("fetched node health", "node", node, "health", ready.Ok)
//		}
//
//		if !ready.Ok && ready.ResourceError != "" {
//			err := errors.New(ready.ResourceError)
//			r.logger.Error(err, "health check reported a node error", "node", node, "health", ready.Ok, "resourceError", ready.ResourceError)
//		} else if !ready.Ok && (ready.ResourceError == "OUT_OF_DISK" || ready.ResourceError == "OUT_OF_MEMORY") {
//			return ConditionReasonQuorumNeedsAttention, 0, fmt.Errorf("health check reported a blocking node error on %s: %s", node, ready.ResourceError)
//		}
//		healthResults[node] = ready.Ok
//	}
//
//	healthyNodes := availableNodes
//	for _, healthy := range healthResults {
//		if !healthy {
//			healthyNodes--
//		}
//	}
//
//	r.logger.V(debugLevel).Info("evaluated quorum", "MinRequiredNodes", q.MinRequiredNodes, "AvailableNodes", q.AvailableNodes, "healthyNodes", healthyNodes)
//
//	if healthyNodes < minRequiredNodes {
//		if sts.Status.ReadyReplicas > 1 {
//			r.logger.Info("downgrading quorum")
//			desiredReplicas := int32(1)
//
//			_, size, err := r.updateConfigMap(ctx, ts, cm, ptr.To[int32](desiredReplicas))
//			if err != nil {
//				return ConditionReasonQuorumNotReady, 0, err
//			}
//
//			err = r.ScaleStatefulSet(ctx, sts, desiredReplicas)
//			if err != nil {
//				return ConditionReasonQuorumNotReady, 0, err
//			}
//
//			return ConditionReasonQuorumDowngraded, size, nil
//		}
//
//		if healthyNodes == 0 && minRequiredNodes == 1 {
//			r.logger.Info("purging quorum")
//			err := r.PurgeStatefulSetPods(ctx, sts)
//			if err != nil {
//				return ConditionReasonQuorumNotReady, 0, err
//			}
//		}
//
//		return ConditionReasonQuorumNotReady, healthyNodes, fmt.Errorf("quorum has %d healthy Nodes, minimum required %d", healthyNodes, minRequiredNodes)
//	}
//
//	if sts.Status.ReadyReplicas < ts.Spec.Replicas {
//		if sts.Status.ReadyReplicas < ts.Spec.Replicas {
//			r.logger.Info("upgrading quorum")
//
//			_, size, err := r.updateConfigMap(ctx, ts, cm, &ts.Spec.Replicas)
//			if err != nil {
//				return ConditionReasonQuorumNotReady, 0, err
//			}
//
//			err = r.ScaleStatefulSet(ctx, sts, ts.Spec.Replicas)
//			if err != nil {
//				return ConditionReasonQuorumNotReady, 0, err
//			}
//
//			return ConditionReasonQuorumUpgraded, size, nil
//		}
//	} else {
//		if int32(healthyNodes) == sts.Status.ReadyReplicas {
//			return ConditionReasonQuorumReady, healthyNodes, nil
//		}
//	}
//
//	return ConditionReasonQuorumReady, healthyNodes, nil
//}

//func (r *TypesenseClusterReconciler) getNodeHealth(httpClient *http.Client, node string, ts *tsv1alpha1.TypesenseCluster) (NodeHealthResponse, error) {
//	fqdn := r.getNodeFullyQualifiedDomainName(ts, node)
//	resp, err := httpClient.Get(fmt.Sprintf("http://%s:%d/health", fqdn, ts.Spec.ApiPort))
//	if err != nil {
//		return NodeHealthResponse{Ok: false}, err
//	}
//	defer resp.Body.Close()
//
//	body, err := io.ReadAll(resp.Body)
//	if err != nil {
//		return NodeHealthResponse{Ok: false}, err
//	}
//
//	var nodeHealthResponse NodeHealthResponse
//	err = json.Unmarshal(body, &nodeHealthResponse)
//	if err != nil {
//		return NodeHealthResponse{Ok: false}, err
//	}
//
//	return nodeHealthResponse, nil
//}
