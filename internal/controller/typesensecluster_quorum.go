package controller

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	QuorumReadinessGateCondition = "RaftQuorumReady"
	HealthyWriteLagKey           = "TYPESENSE_HEALTHY_WRITE_LAG"
	HealthyWriteLagDefaultValue  = 500
	HealthyReadLagKey            = "TYPESENSE_HEALTHY_READ_LAG"
	HealthyReadLagDefaultValue   = 1000
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

	if quorum.AvailableNodes != int(ts.Spec.Replicas) {
		r.logger.Info("resizing quorum pending", "size", ts.Spec.Replicas)
	}

	unscheduledPods, _ := r.GetUnscheduledPods(ctx, sts)
	if len(unscheduledPods) > 0 {
		_ = r.RestartUnscheduledPods(ctx, unscheduledPods, ts)
	}

	if quorum.AvailableNodes < quorum.MinRequiredNodes {
		return ConditionReasonStatefulSetNotReady, 0, nil
	}

	nodesStatus := make(map[string]NodeStatus)
	httpClient, err := r.getHttpClient(ts)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	queuedWrites := 0
	healthyWriteLagThreshold := r.getHealthyWriteLagThreshold(ctx, ts)

	nodeKeys := make([]string, 0, len(quorum.Nodes))
	for k := range quorum.Nodes {
		nodeKeys = append(nodeKeys, k)
	}
	sort.Strings(nodeKeys)

	nodeEndpoints := make([]NodeEndpoint, 0, len(quorum.Nodes))
	for _, key := range nodeKeys {
		ne := NodeEndpoint{
			PodName: key,
			IP:      quorum.Nodes[key],
		}

		nodeEndpoints = append(nodeEndpoints, ne)
	}

	logs := make(map[string]string, len(quorum.Nodes))
	for _, ne := range nodeEndpoints {
		l, err := r.getPodLogs(ctx, ne, ts.Namespace)
		if err != nil {
			r.logger.Error(err, "fetching pod logs failed", "node", r.getShortName(ne.PodName), "ip", ne.IP)
		}

		logs[ne.PodName] = l
	}

	//quorum.Nodes are coming straight from the PodList of Statefulset
	for _, ne := range nodeEndpoints {
		status, err := r.getNodeStatus(ctx, httpClient, ne, ts, secret, logs[ne.PodName])
		if err != nil {
			r.logger.Error(err, "fetching node status failed", "node", r.getShortName(ne.PodName), "ip", ne.IP)
		}

		if status.QueuedWrites > 0 && queuedWrites < status.QueuedWrites {
			queuedWrites = status.QueuedWrites
		}

		r.logger.V(debugLevel).Info(
			"reporting node status",
			"node",
			r.getShortName(ne.PodName),
			"state",
			status.State,
			"ip",
			ne.IP,
			"queued_writes",
			status.QueuedWrites,
			"commited_index",
			status.CommittedIndex,
		)
		nodesStatus[ne.PodName] = status
	}

	clusterStatus := r.getClusterStatus(nodesStatus)
	r.logger.V(debugLevel).Info("reporting cluster status", "status", clusterStatus)

	if clusterStatus == ClusterStatusSplitBrain {
		nodeslist := strings.Split(quorum.NodesListConfigMap.Data["fallback"], ",")
		stsPrefix := fmt.Sprintf(ClusterStatefulSet, ts.Name)
		if containsPrefix(nodeslist, stsPrefix) {
			return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
		}
		return r.downgradeQuorum(ctx, ts, quorum.NodesListConfigMap, stsObjectKey, sts.Status.ReadyReplicas, int32(quorum.MinRequiredNodes))
	}

	clusterNeedsAttention := false
	nodesHealth := make(map[string]bool)

	for _, ne := range nodeEndpoints {
		key := ne.PodName
		nodeStatus := nodesStatus[key]

		condition := r.calculatePodReadinessGate(ctx, httpClient, ne, nodeStatus, ts, logs[key])
		if condition.Reason == string(nodeNotRecoverable) {
			clusterNeedsAttention = true
		}

		nodesHealth[key], _ = strconv.ParseBool(string(condition.Status))

		podPrefix := fmt.Sprintf(ClusterStatefulSet, ts.Name)
		podIndex := strings.Replace(key, fmt.Sprintf("%s-", podPrefix), "", -1)
		podName := fmt.Sprintf("%s-%s", podPrefix, podIndex)
		podObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: podName}

		err = r.updatePodReadinessGate(ctx, podObjectKey, condition)
		if err != nil {
			r.logger.Error(err, fmt.Sprintf("unable to update statefulset pod: %s", podObjectKey.Name))
			return ConditionReasonQuorumNotReady, 0, err
		}
	}

	if clusterNeedsAttention {
		return ConditionReasonQuorumNeedsAttentionMemoryOrDiskIssue, 0, nil
	}

	minRequiredNodes := quorum.MinRequiredNodes
	availableNodes := quorum.AvailableNodes
	healthyNodes := 0

	for _, healthy := range nodesHealth {
		if healthy {
			healthyNodes++
		}
	}

	r.logger.Info("evaluated quorum", "minRequiredNodes", minRequiredNodes, "availableNodes", availableNodes, "healthyNodes", healthyNodes)

	if (queuedWrites > healthyWriteLagThreshold) && healthyNodes > 0 {
		return ConditionReasonQuorumNeedsAttentionClusterIsLagging, 0, nil
	}

	if clusterStatus == ClusterStatusElectionDeadlock {
		nodeslist := strings.Split(quorum.NodesListConfigMap.Data["fallback"], ",")
		stsPrefix := fmt.Sprintf(ClusterStatefulSet, ts.Name)
		if containsPrefix(nodeslist, stsPrefix) {
			return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
		}
		return r.downgradeQuorum(ctx, ts, quorum.NodesListConfigMap, stsObjectKey, int32(healthyNodes), int32(minRequiredNodes))
	}

	if clusterStatus == ClusterStatusNotReady {
		// Never purge all pods — killing the last pod makes recovery impossible
		// and defeats the purpose of running a multi-node cluster.
		// Instead, wait for the pod(s) to recover on their own.
		return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
	}

	if clusterStatus == ClusterStatusOK && *sts.Spec.Replicas < ts.Spec.Replicas {
		if queuedWrites > 0 {
			return ConditionReasonQuorumQueuedWrites, 0, nil
		}

		return r.upgradeQuorum(ctx, ts, quorum.NodesListConfigMap, stsObjectKey)
	}

	if healthyNodes < minRequiredNodes {
		return ConditionReasonQuorumNotReady, 0, nil
	}

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

	_, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	// Never scale below the minimum required nodes for quorum.
	// For a 3-node cluster, minRequiredNodes = 2. Scaling to 1 destroys
	// quorum and makes Raft leader election impossible, leaving the cluster
	// in an unrecoverable state.
	desiredReplicas := minRequiredNodes
	if desiredReplicas < 1 {
		desiredReplicas = 1
	}

	r.logger.Info("downgrade target", "desiredReplicas", desiredReplicas, "minRequiredNodes", minRequiredNodes)

	err = r.ScaleStatefulSet(ctx, stsObjectKey, desiredReplicas)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	// Never purge all pods — this kills every running instance and defeats
	// the purpose of running a multi-node cluster for high availability.

	_, size, updated, err := r.updateConfigMap(ctx, ts, cm, ptr.To[int32](desiredReplicas), true)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	if updated && ts.Spec.ForceResetPeersConfigOnUpdate {
		_ = r.forcePodsConfigMapUpdate(ctx, ts)
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

	err = r.ScaleStatefulSet(ctx, stsObjectKey, size)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	_, _, updated, err := r.updateConfigMap(ctx, ts, cm, &size, true)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	if updated && ts.Spec.ForceResetPeersConfigOnUpdate {
		_ = r.forcePodsConfigMapUpdate(ctx, ts)
	}

	return ConditionReasonQuorumUpgraded, int(size), nil
}

type readinessGateReason string

const (
	nodeHealthy        readinessGateReason = "NodeHealthy"
	nodeNotHealthy     readinessGateReason = "NodeNotHealthy"
	nodeNotRecoverable readinessGateReason = "NodeNotRecoverable"
)

func (r *TypesenseClusterReconciler) calculatePodReadinessGate(ctx context.Context, httpClient *http.Client, node NodeEndpoint, nodeStatus NodeStatus, ts *tsv1alpha1.TypesenseCluster, logs string) *v1.PodCondition {
	conditionReason := nodeHealthy
	conditionMessage := fmt.Sprintf("node's role is now: %s", nodeStatus.State)
	conditionStatus := v1.ConditionTrue

	health, err := r.getNodeHealth(ctx, httpClient, node, ts, logs)
	if err != nil {
		conditionReason = nodeNotHealthy
		conditionStatus = v1.ConditionFalse

		r.logger.Error(err, "fetching node health failed", "node", r.getShortName(node.PodName), "ip", node.IP)
	} else {
		if !health.Ok {
			if health.ResourceError != nil && (*health.ResourceError == OutOfMemory || *health.ResourceError == OutOfDisk) {
				conditionReason = nodeNotRecoverable
				conditionMessage = fmt.Sprintf("node is failing: %s", string(*health.ResourceError))
				conditionStatus = v1.ConditionFalse

				err := fmt.Errorf("health check reported a blocking node error on %s: %s", r.getShortName(node.PodName), string(*health.ResourceError))
				r.logger.Error(err, "quorum cannot be recovered automatically")
			}

			conditionReason = nodeNotHealthy
			conditionStatus = v1.ConditionFalse
		}
	}

	r.logger.V(debugLevel).Info("reporting node health", "node", r.getShortName(node.PodName), "healthy", health.Ok, "ip", node.IP)
	condition := &v1.PodCondition{
		Type:    QuorumReadinessGateCondition,
		Status:  conditionStatus,
		Reason:  string(conditionReason),
		Message: conditionMessage,
	}

	return condition
}

func (r *TypesenseClusterReconciler) updatePodReadinessGate(ctx context.Context, podObjectKey client.ObjectKey, condition *v1.PodCondition) error {
	pod := &v1.Pod{}
	err := r.Get(ctx, podObjectKey, pod)
	if err != nil {
		r.logger.Error(err, fmt.Sprintf("unable to fetch statefulset pod: %s", podObjectKey.Name))
		return nil
	}

	patch := client.MergeFrom(pod.DeepCopy())

	found := false
	var updatedConditions []v1.PodCondition
	for _, c := range pod.Status.Conditions {
		if c.Type == condition.Type {
			if !found {
				updatedConditions = append(updatedConditions, *condition)
				found = true
			}
		} else {
			updatedConditions = append(updatedConditions, c)
		}
	}
	if !found {
		updatedConditions = append(updatedConditions, *condition)
	}

	pod.Status.Conditions = updatedConditions

	if err := r.Status().Patch(ctx, pod, patch); err != nil {
		r.logger.Error(err, "updating pod readiness gate condition failed", "pod", pod.Name)
		return err
	}

	//r.logger.V(debugLevel).Info("updating pod readiness gate condition", "pod", pod.Name, "condition", condition.Type, "conditionStatus", condition.Status)
	return nil
}
