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

func (r *TypesenseClusterReconciler) ReconcileQuorum(ctx context.Context, ts tsv1alpha1.TypesenseCluster, sts appsv1.StatefulSet) (ConditionQuorum, int, error) {
	r.logger.Info("reconciling quorum")

	if sts.Status.ReadyReplicas != sts.Status.Replicas {
		return ConditionReasonStatefulSetNotReady, 0, fmt.Errorf("statefulset not ready: %d/%d replicas ready", sts.Status.ReadyReplicas, sts.Status.Replicas)
	}

	condition, size, err := r.getQuorumHealth(ctx, &ts, &sts)
	r.logger.Info("reconciling quorum completed", "condition", condition)
	return condition, size, err
}

func (r *TypesenseClusterReconciler) getQuorumHealth(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet) (ConditionQuorum, int, error) {
	configMapName := fmt.Sprintf(ClusterNodesConfigMap, ts.Name)
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		r.logger.Error(err, fmt.Sprintf("unable to fetch config map: %s", configMapName))
		return ConditionReasonQuorumNotReady, 0, err
	}

	nodes := strings.Split(cm.Data["nodes"], ",")
	availableNodes := len(nodes)
	minRequiredNodes := (availableNodes-1)/2 + 1
	if availableNodes < minRequiredNodes {
		return ConditionReasonQuorumNotReady, availableNodes, fmt.Errorf("quorum has less than minimum %d available nodes", minRequiredNodes)
	}

	healthResults := make(map[string]bool, availableNodes)
	httpClient := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	for _, node := range nodes {
		fqdn := r.getNodeFullyQualifiedDomainName(ts, node)
		resp, err := httpClient.Get(fmt.Sprintf("http://%s:%d/health", fqdn, ts.Spec.ApiPort))
		if err != nil {
			r.logger.Error(err, "health check failed", "node", node)
			healthResults[node] = false
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			r.logger.Error(err, "reading health check response failed", "node", node)
			healthResults[node] = false
			continue
		}

		var ready NodeHealthResponse
		err = json.Unmarshal(body, &ready)
		if err != nil {
			r.logger.Error(err, "unmarshalling health check response failed", "node", node)
			healthResults[node] = false
			continue
		}

		if !ready.Ok && ready.ResourceError != "" {
			err := errors.New(ready.ResourceError)
			r.logger.Error(err, "health check reported a node error", "node", node)
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
	} else {
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
