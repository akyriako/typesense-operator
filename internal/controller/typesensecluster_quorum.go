package controller

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	QuorumReadinessGateCondition = "RaftQuorumReady"
	HealthyWriteLagKey           = "TYPESENSE_HEALTHY_WRITE_LAG"
	HealthyWriteLagDefaultValue  = 500
	HealthyReadLagKey            = "TYPESENSE_HEALTHY_READ_LAG"
	HealthyReadLagDefaultValue   = 1000
)

//nolint:gocyclo // ReconcileQuorum coordinates multiple steps safely
func (r *TypesenseClusterReconciler) ReconcileQuorum(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, secret *v1.Secret, stsObjectKey client.ObjectKey) (ConditionQuorum, int, error) {
	log.FromContext(ctx).Info("reconciling quorum health")

	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	quorum, err := r.getQuorum(ctx, ts, sts)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	log.FromContext(ctx).Info("calculated quorum", "minRequiredNodes", quorum.MinRequiredNodes, "availableNodes", quorum.AvailableNodes)

	if quorum.AvailableNodes != int(ts.Spec.Replicas) {
		log.FromContext(ctx).Info("resizing quorum pending", "size", ts.Spec.Replicas)
	}

	unscheduledPods, _ := r.GetUnscheduledPods(ctx, sts)
	if len(unscheduledPods) > 0 {
		_ = r.RestartUnscheduledPods(ctx, unscheduledPods, ts)
	}

	if quorum.AvailableNodes < quorum.MinRequiredNodes {
		return ConditionReasonStatefulSetNotReady, 0, nil
	}

	httpClient := r.getHttpClient()
	_, healthyWriteLagThreshold := r.getHealthyLagThresholds(ctx, ts)

	nodeKeys := make([]string, 0, len(quorum.Nodes))
	for k := range quorum.Nodes {
		nodeKeys = append(nodeKeys, k)
	}
	sort.Strings(nodeKeys)

	// quorum.Nodes are coming straight from the PodList of Statefulset
	nodeEndpoints := make([]NodeEndpoint, 0, len(quorum.Nodes))
	for _, key := range nodeKeys {
		nodeEndpoints = append(nodeEndpoints, quorum.Nodes[key])
	}

	probes := r.probeNodes(ctx, httpClient, nodeEndpoints, ts, secret)

	nodesStatus := make(map[string]NodeStatus, len(nodeEndpoints))
	queuedWrites := 0
	for i, ne := range nodeEndpoints {
		status := probes[i].status
		if status.QueuedWrites > queuedWrites {
			queuedWrites = status.QueuedWrites
		}

		log.FromContext(ctx).V(debugLevel).Info(
			"reporting node status",
			"node",
			r.getShortName(ne.PodName),
			"state",
			status.State,
			"ip",
			ne.IP,
			"queued_writes",
			status.QueuedWrites,
			"committed_index",
			status.CommittedIndex,
		)
		nodesStatus[ne.PodName] = status
	}

	clusterStatus := r.getClusterStatus(nodesStatus)
	log.FromContext(ctx).V(debugLevel).Info("reporting cluster status", "status", clusterStatus)

	clusterNeedsAttention := false
	nodesHealth := make(map[string]bool)

	for i, ne := range nodeEndpoints {
		key := ne.PodName

		condition := probes[i].readinessGate
		if condition.Reason == string(nodeNotRecoverable) {
			clusterNeedsAttention = true
		}

		nodesHealth[key], _ = strconv.ParseBool(string(condition.Status))

		podObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: key}

		err = r.updatePodReadinessGate(ctx, podObjectKey, condition)
		if err != nil {
			log.FromContext(ctx).Error(err, fmt.Sprintf("unable to update statefulset pod: %s", podObjectKey.Name))
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

	if clusterStatus == ClusterStatusSplitBrain {
		return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
	}

	log.FromContext(ctx).Info("evaluated quorum", "minRequiredNodes", minRequiredNodes, "availableNodes", availableNodes, "healthyNodes", healthyNodes)

	if (queuedWrites > healthyWriteLagThreshold) && healthyNodes > 0 {
		return ConditionReasonQuorumNeedsAttentionClusterIsLagging, 0, nil
	}

	if clusterStatus == ClusterStatusElectionDeadlock {
		return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
	}

	if clusterStatus == ClusterStatusNotReady {
		if availableNodes == 1 {
			podName := fmt.Sprintf("%s-%d", fmt.Sprintf(ClusterStatefulSet, ts.Name), 0)
			nodeStatus := nodesStatus[podName]

			pod := &v1.Pod{}
			if err := r.Get(ctx, client.ObjectKey{Namespace: ts.Namespace, Name: podName}, pod); err != nil {
				return ConditionReasonQuorumNotReady, 0, client.IgnoreNotFound(err)
			}

			if shouldPurgeSingleNode(pod, nodeStatus.State, time.Now()) {
				log.FromContext(ctx).Info("purging quorum")
				err := r.PurgeStatefulSetPods(ctx, sts, ts)
				if err != nil {
					return ConditionReasonQuorumNotReady, 0, err
				}

				return ConditionReasonQuorumNotReady, 0, nil
			}

			return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
		}

		if minRequiredNodes > healthyNodes {
			return ConditionReasonQuorumNotReadyWaitATerm, 0, nil
		}
	}

	if clusterStatus == ClusterStatusOK && ptr.Deref(sts.Spec.Replicas, 1) < ts.Spec.Replicas {
		if queuedWrites > 0 {
			return ConditionReasonQuorumQueuedWrites, 0, nil
		}

		return r.upgradeQuorum(ctx, ts, quorum.NodesListConfigMap, stsObjectKey)
	}

	if healthyNodes < minRequiredNodes {
		return ConditionReasonQuorumNotReady, 0, nil
	}

	if sts.Status.UpdateRevision != sts.Status.CurrentRevision {
		return ConditionReasonUpgrading, 0, nil
	}

	if sts.Status.Replicas < *sts.Spec.Replicas {
		return ConditionReasonUpgrading, 0, nil
	}

	if healthyNodes < int(ts.Spec.Replicas) {
		return ConditionReasonDegraded, 0, nil
	}

	return ConditionReasonQuorumReady, 0, nil
}

type nodeProbe struct {
	status        NodeStatus
	readinessGate *v1.PodCondition
}

// probeNodes fetches logs, status and health of all nodes in parallel. Each request waits for its own
// timeout, so probing the nodes one after another would add those timeouts up and block the worker.
func (r *TypesenseClusterReconciler) probeNodes(ctx context.Context, httpClient *http.Client, nodes []NodeEndpoint, ts *tsv1alpha1.TypesenseCluster, secret *v1.Secret) []nodeProbe {
	probes := make([]nodeProbe, len(nodes))

	var wg sync.WaitGroup
	for i, ne := range nodes {
		wg.Go(func() {
			logs, err := r.getPodLogs(ctx, ne, ts.Namespace)
			if err != nil {
				log.FromContext(ctx).Error(err, "fetching pod logs failed", "node", r.getShortName(ne.PodName), "ip", ne.IP)
			}

			status, err := r.getNodeStatus(ctx, httpClient, ne, ts, secret, logs)
			if err != nil {
				log.FromContext(ctx).Error(err, "fetching node status failed", "node", r.getShortName(ne.PodName), "ip", ne.IP)
			}

			probes[i] = nodeProbe{
				status:        status,
				readinessGate: r.calculatePodReadinessGate(ctx, httpClient, ne, status, ts, logs),
			}
		})
	}
	wg.Wait()

	return probes
}

// shouldPurgeSingleNode decides whether the only node of a cluster is stuck. A node in ERROR state is
// purged right away. An unreachable node is only purged once its Typesense container has been running
// for the startup grace period: a pod already has its IP while the image is still being pulled or the
// server is still starting, and purging it then would restart that pod over and over.
func shouldPurgeSingleNode(pod *v1.Pod, state NodeState, now time.Time) bool {
	switch state {
	case ErrorState:
		return true
	case UnreachableState:
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == containerTypesense && cs.State.Running != nil {
				return now.Sub(cs.State.Running.StartedAt.Time) >= singleNodeStartupGracePeriod
			}
		}
		return false
	default:
		return false
	}
}

var (
	QuorumUpgraded UpdateStatefulSetTrigger = "QuorumUpgraded"
)

func (r *TypesenseClusterReconciler) upgradeQuorum(
	ctx context.Context,
	ts *tsv1alpha1.TypesenseCluster,
	cm *v1.ConfigMap,
	stsObjectKey client.ObjectKey,
) (ConditionQuorum, int, error) {
	// log.FromContext(ctx).Info("upgrading quorum", "incremental", ts.Spec.IncrementalQuorumRecovery)
	log.FromContext(ctx).V(debugLevel).Info("scaling statefulset", "sts", stsObjectKey.Name, "triggers", QuorumUpgraded, "incremental", ts.Spec.IncrementalQuorumRecovery)

	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}
	size := ts.Spec.Replicas
	if ts.Spec.IncrementalQuorumRecovery {
		currentSize := ptr.Deref(sts.Spec.Replicas, 1)
		size = currentSize + 1
	}

	err = r.ScaleStatefulSet(ctx, stsObjectKey, size)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	updated, err := r.updateConfigMap(ctx, ts, cm, &size, true)
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

		log.FromContext(ctx).Error(err, "fetching node health failed", "node", r.getShortName(node.PodName), "ip", node.IP)
	} else {
		if !health.Ok {
			if health.ResourceError != nil && (*health.ResourceError == OutOfMemory || *health.ResourceError == OutOfDisk) {
				conditionReason = nodeNotRecoverable
				conditionMessage = fmt.Sprintf("node is failing: %s", string(*health.ResourceError))
				conditionStatus = v1.ConditionFalse

				err := fmt.Errorf("health check reported a blocking node error on %s: %s", r.getShortName(node.PodName), string(*health.ResourceError))
				log.FromContext(ctx).Error(err, "quorum cannot be recovered automatically")
			} else {
				conditionReason = nodeNotHealthy
				conditionStatus = v1.ConditionFalse
			}
		}
	}

	log.FromContext(ctx).V(debugLevel).Info("reporting node health", "node", r.getShortName(node.PodName), "healthy", health.Ok, "ip", node.IP)
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
		log.FromContext(ctx).Error(err, fmt.Sprintf("unable to fetch pod: %s", podObjectKey.Name))
		return client.IgnoreNotFound(err)
	}

	// Skip the status patch (one API request per pod and reconcile) when nothing changed.
	for _, c := range pod.Status.Conditions {
		if c.Type == condition.Type && c.Status == condition.Status && c.Reason == condition.Reason && c.Message == condition.Message {
			return nil
		}
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
		log.FromContext(ctx).Error(err, "updating pod readiness gate condition failed", "pod", pod.Name)
		return err
	}

	// log.FromContext(ctx).V(debugLevel).Info("updating pod readiness gate condition", "pod", pod.Name, "condition", condition.Type, "conditionStatus", condition.Status)
	return nil
}
