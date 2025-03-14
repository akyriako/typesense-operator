package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"time"
)

const (
	PodReadinessGateCondition = "RaftQuorumReady"
)

func (r *TypesenseClusterReconciler) ReconcileQuorum(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, secret *v1.Secret, stsObjectKey client.ObjectKey) (ConditionQuorum, int, error) {
	r.logger.Info("reconciling quorum health")

	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	quorum, err := r.getQuorum(ctx, ts, sts)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	r.logger.Info("calculated quorum", "minRequiredNodes", quorum.MinRequiredNodes, "availableNodes", quorum.AvailableNodes)

	nodesStatus := make(map[string]NodeStatus)
	httpClient := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	for _, node := range quorum.Nodes {
		status, err := r.getNodeStatus(ctx, httpClient, node, ts, secret)
		if err != nil {
			r.logger.Error(err, "fetching node status failed", "node", r.getShortName(node))
		}

		r.logger.V(debugLevel).Info(
			"reporting node status",
			"node",
			r.getShortName(node),
			"state",
			status.State,
			"queued_writes",
			status.QueuedWrites,
			"commited_index",
			status.CommittedIndex,
		)
		nodesStatus[node] = status
	}

	clusterStatus := r.getClusterStatus(nodesStatus)
	r.logger.V(debugLevel).Info("reporting cluster status", "status", clusterStatus)

	if clusterStatus == ClusterStatusSplitBrain {
		return r.downgradeQuorum(ctx, ts, quorum.NodesListConfigMap, stsObjectKey, sts.Status.ReadyReplicas, int32(quorum.MinRequiredNodes))
	}

	clusterNeedsAttention := false
	nodesHealth := make(map[string]bool)

	for n, node := range quorum.Nodes {
		nodeStatus := nodesStatus[node]

		condition := r.calculatePodReadinessGate(ctx, httpClient, node, nodeStatus, ts)
		if condition.Reason == string(nodeNotRecoverable) {
			clusterNeedsAttention = true
		}

		// TODO if condition.Reason == string(nodeIsLagging) {

		nodesHealth[node], _ = strconv.ParseBool(string(condition.Status))

		podName := fmt.Sprintf("%s-%d", fmt.Sprintf(ClusterStatefulSet, ts.Name), n)
		podObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: podName}

		err = r.updatePodReadinessGate(ctx, podObjectKey, condition, ts)
		if err != nil {
			r.logger.Error(err, fmt.Sprintf("unable to update statefulset pod: %s", podObjectKey.Name))
			return ConditionReasonQuorumNotReady, 0, err
		}
	}

	if clusterNeedsAttention {
		return ConditionReasonQuorumNeedsAttention, 0, fmt.Errorf("cluster needs administrative attention")
	}

	minRequiredNodes := quorum.MinRequiredNodes
	availableNodes := quorum.AvailableNodes
	healthyNodes := availableNodes

	for _, healthy := range nodesHealth {
		if !healthy {
			healthyNodes--
		}
	}

	r.logger.Info("evaluated quorum", "minRequiredNodes", minRequiredNodes, "availableNodes", availableNodes, "healthyNodes", healthyNodes)

	if clusterStatus == ClusterStatusElectionDeadlock {
		return r.downgradeQuorum(ctx, ts, quorum.NodesListConfigMap, stsObjectKey, int32(healthyNodes), int32(minRequiredNodes))
	}

	if clusterStatus == ClusterStatusNotReady {
		if availableNodes == 1 {
			return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
		}

		if minRequiredNodes > healthyNodes {
			return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
		}
	}

	//// TODO && !clusterIsLagging {
	//// BUG LEADER+healthy == false skips this loop and return a healthy cluster
	//if clusterStatus == ClusterStatusNotReady && healthyNodes < minRequiredNodes {
	//	return r.downgradeQuorum(ctx, ts, quorum.NodesListConfigMap, sts, int32(healthyNodes), int32(minRequiredNodes))
	//}

	if clusterStatus == ClusterStatusOK && *sts.Spec.Replicas < ts.Spec.Replicas {
		return r.upgradeQuorum(ctx, ts, quorum.NodesListConfigMap, stsObjectKey)
	}

	if healthyNodes < minRequiredNodes {
		return ConditionReasonQuorumNotReady, 0, nil
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

func (r *TypesenseClusterReconciler) downgradeQuorum(
	ctx context.Context,
	ts *tsv1alpha1.TypesenseCluster,
	cm *v1.ConfigMap,
	stsObjectKey client.ObjectKey,
	healthyNodes, minRequiredNodes int32,
) (ConditionQuorum, int, error) {
	r.logger.Info("downgrading quorum")

	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	if healthyNodes == 0 && minRequiredNodes == 1 {
		r.logger.Info("purging quorum")
		err := r.PurgeStatefulSetPods(ctx, sts)
		if err != nil {
			return ConditionReasonQuorumNotReady, 0, err
		}

		return ConditionReasonQuorumNotReady, 0, nil
	}

	desiredReplicas := int32(1)

	_, size, err := r.updateConfigMap(ctx, ts, cm, ptr.To[int32](desiredReplicas))
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	err = r.ScaleStatefulSet(ctx, sts, desiredReplicas)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	return ConditionReasonQuorumDowngraded, size, nil
}

func (r *TypesenseClusterReconciler) upgradeQuorum(
	ctx context.Context,
	ts *tsv1alpha1.TypesenseCluster,
	cm *v1.ConfigMap,
	stsObjectKey client.ObjectKey,
) (ConditionQuorum, int, error) {
	r.logger.Info("upgrading quorum", "incremental", ts.Spec.IncrementalQuorumRecovery)

	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}
	size := ts.Spec.Replicas
	if ts.Spec.IncrementalQuorumRecovery {
		size = sts.Status.Replicas + 1
	}

	_, _, err = r.updateConfigMap(ctx, ts, cm, &size)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	err = r.ScaleStatefulSet(ctx, sts, size)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	return ConditionReasonQuorumUpgraded, int(size), nil
}

type readinessGateReason string

const (
	nodeHealthy        readinessGateReason = "NodeHealthy"
	nodeNotHealthy     readinessGateReason = "NodeNotHealthy"
	nodeNotRecoverable readinessGateReason = "NodeNotRecoverable"
)

func (r *TypesenseClusterReconciler) calculatePodReadinessGate(ctx context.Context, httpClient *http.Client, node string, nodeStatus NodeStatus, ts *tsv1alpha1.TypesenseCluster) *v1.PodCondition {
	conditionReason := nodeHealthy
	conditionMessage := fmt.Sprintf("node's role is now: %s", nodeStatus.State)
	conditionStatus := v1.ConditionTrue

	health, err := r.getNodeHealth(ctx, httpClient, node, ts)
	if err != nil {
		conditionReason = nodeNotHealthy
		conditionStatus = v1.ConditionFalse

		r.logger.Error(err, "fetching node health failed", "node", r.getShortName(node))
	} else {
		if !health.Ok {
			if health.ResourceError != nil && (*health.ResourceError == OutOfMemory || *health.ResourceError == OutOfDisk) {
				conditionReason = nodeNotRecoverable
				conditionMessage = fmt.Sprintf("node is failing: %s", string(*health.ResourceError))
				conditionStatus = v1.ConditionFalse

				err := fmt.Errorf("health check reported a blocking node error on %s: %s", r.getShortName(node), string(*health.ResourceError))
				r.logger.Error(err, "quorum cannot be recovered automatically")
			}

			conditionReason = nodeNotHealthy
			conditionStatus = v1.ConditionFalse
		}
	}

	r.logger.V(debugLevel).Info("reporting node health", "node", r.getShortName(node), "healthy", health.Ok)
	condition := &v1.PodCondition{
		Type:    PodReadinessGateCondition,
		Status:  conditionStatus,
		Reason:  string(conditionReason),
		Message: conditionMessage,
	}

	return condition
}

func (r *TypesenseClusterReconciler) updatePodReadinessGate(ctx context.Context, podObjectKey client.ObjectKey, condition *v1.PodCondition, ts *tsv1alpha1.TypesenseCluster) error {

	pod := &v1.Pod{}
	err := r.Get(ctx, podObjectKey, pod)
	if err != nil {
		r.logger.Error(err, fmt.Sprintf("unable to fetch statefulset pod: %s", podObjectKey.Name))
		return nil
	}

	patch := client.MergeFrom(pod.DeepCopy())
	//r.logger.V(debugLevel).Info("updating pod readiness gate condition", "pod", pod.Name, "condition", condition.Type, "status", condition.Status)

	updated := false
	for i, c := range pod.Status.Conditions {
		if c.Type == PodReadinessGateCondition && pod.Status.Conditions[i] != *condition {
			pod.Status.Conditions[i] = *condition
			updated = true
			break
		}
	}
	if !updated {
		pod.Status.Conditions = append(pod.Status.Conditions, *condition)
	}

	if err := r.Status().Patch(ctx, pod, patch); err != nil {
		r.logger.Error(err, "updating pod readiness gate condition failed", "pod", pod.Name)
		return err
	}

	return nil
}
