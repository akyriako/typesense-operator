package controller

import (
	"context"
	"encoding/json"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/pkg/errors"
	"io"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

type NodeHealthResponse struct {
	Ok            bool   `json:"ok"`
	ResourceError string `json:"resource_error"`
}

type Quorum struct {
	minRequiredNodes int
	availableNodes   int
	nodes            []string
	cm               *v1.ConfigMap
}

func (r *TypesenseClusterReconciler) ReconcileQuorum(ctx context.Context, ts tsv1alpha1.TypesenseCluster, sts appsv1.StatefulSet) (ConditionQuorum, int, error) {
	r.logger.Info("reconciling quorum")

	q, err := r.getQuorum(ctx, &ts)
	if err != nil {
		return ConditionReasonQuorumNotReady, 0, err
	}

	r.logger.V(debugLevel).Info("calculating quorum", "minRequiredNodes", q.minRequiredNodes, "availableNodes", q.availableNodes)

	if sts.Status.ReadyReplicas != sts.Status.Replicas && (sts.Status.ReadyReplicas < int32(q.minRequiredNodes) && q.minRequiredNodes > 1) {
		return ConditionReasonStatefulSetNotReady, 0, fmt.Errorf("statefulset not ready: %d/%d replicas ready", sts.Status.ReadyReplicas, sts.Status.Replicas)
	}

	condition, size, err := r.getQuorumHealth(ctx, &ts, &sts, q)
	r.logger.Info("reconciling quorum completed", "condition", condition)
	return condition, size, err
}

func (r *TypesenseClusterReconciler) getQuorum(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (*Quorum, error) {
	configMapName := fmt.Sprintf(ClusterNodesConfigMap, ts.Name)
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		r.logger.Error(err, fmt.Sprintf("unable to fetch config map: %s", configMapName))
		return &Quorum{}, err
	}

	nodes := strings.Split(cm.Data["nodes"], ",")
	availableNodes := len(nodes)
	minRequiredNodes := (availableNodes-1)/2 + 1

	return &Quorum{minRequiredNodes, availableNodes, nodes, cm}, nil
}

func (r *TypesenseClusterReconciler) getQuorumHealth(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet, q *Quorum) (ConditionQuorum, int, error) {
	availableNodes := q.availableNodes
	minRequiredNodes := q.minRequiredNodes
	nodes := q.nodes
	cm := q.cm

	if availableNodes < minRequiredNodes {
		return ConditionReasonQuorumNotReady, availableNodes, fmt.Errorf("quorum has less than minimum %d available nodes", minRequiredNodes)
	}

	healthResults := make(map[string]bool, availableNodes)
	httpClient := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	for _, node := range nodes {
		ready, err := r.getNodeHealth(httpClient, node, ts)
		if err != nil {
			r.logger.Error(err, "health check failed", "node", node, "health", false)
		} else {
			r.logger.V(debugLevel).Info("fetched node health", "node", node, "health", ready.Ok)
		}

		if !ready.Ok && ready.ResourceError != "" {
			err := errors.New(ready.ResourceError)
			r.logger.Error(err, "health check reported a node error", "node", node, "health", ready.Ok, "resourceError", ready.ResourceError)
		} else if !ready.Ok && (ready.ResourceError == "OUT_OF_DISK" || ready.ResourceError == "OUT_OF_MEMORY") {
			return ConditionReasonQuorumNeedsAttention, 0, fmt.Errorf("health check reported a blocking node error on %s: %s", node, ready.ResourceError)
		}
		healthResults[node] = ready.Ok
	}

	healthyNodes := availableNodes
	for _, healthy := range healthResults {
		if !healthy {
			healthyNodes--
		}
	}

	r.logger.V(debugLevel).Info("evaluated quorum", "minRequiredNodes", q.minRequiredNodes, "availableNodes", q.availableNodes, "healthyNodes", healthyNodes)

	if healthyNodes < minRequiredNodes {
		if sts.Status.ReadyReplicas > 1 {
			r.logger.Info("downgrading quorum")
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

		if healthyNodes == 0 && minRequiredNodes == 1 {
			r.logger.Info("purging quorum")
			err := r.PurgeStatefulSetPods(ctx, sts)
			if err != nil {
				return ConditionReasonQuorumNotReady, 0, err
			}
		}

		return ConditionReasonQuorumNotReady, healthyNodes, fmt.Errorf("quorum has %d healthy nodes, minimum required %d", healthyNodes, minRequiredNodes)
	}

	if int32(healthyNodes) == sts.Status.ReadyReplicas {
		return ConditionReasonQuorumReady, healthyNodes, nil
	}

	if sts.Status.ReadyReplicas < ts.Spec.Replicas {
		if sts.Status.ReadyReplicas < ts.Spec.Replicas {
			r.logger.Info("upgrading quorum")

			_, size, err := r.updateConfigMap(ctx, ts, cm, &ts.Spec.Replicas)
			if err != nil {
				return ConditionReasonQuorumNotReady, 0, err
			}

			err = r.ScaleStatefulSet(ctx, sts, ts.Spec.Replicas)
			if err != nil {
				return ConditionReasonQuorumNotReady, 0, err
			}

			return ConditionReasonQuorumUpgraded, size, nil
		}
	}

	return ConditionReasonQuorumReady, healthyNodes, nil
}

func (r *TypesenseClusterReconciler) getNodeHealth(httpClient *http.Client, node string, ts *tsv1alpha1.TypesenseCluster) (NodeHealthResponse, error) {
	fqdn := r.getNodeFullyQualifiedDomainName(ts, node)
	resp, err := httpClient.Get(fmt.Sprintf("http://%s:%d/health", fqdn, ts.Spec.ApiPort))
	if err != nil {
		return NodeHealthResponse{Ok: false}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeHealthResponse{Ok: false}, err
	}

	var nodeHealthResponse NodeHealthResponse
	err = json.Unmarshal(body, &nodeHealthResponse)
	if err != nil {
		return NodeHealthResponse{Ok: false}, err
	}

	return nodeHealthResponse, nil
}
