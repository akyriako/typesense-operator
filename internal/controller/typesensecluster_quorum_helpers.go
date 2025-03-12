package controller

import (
	"context"
	"encoding/json"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"io"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

func (r *TypesenseClusterReconciler) getNodeStatus(ctx context.Context, httpClient *http.Client, node string, ts *tsv1alpha1.TypesenseCluster, secret *v1.Secret) (NodeStatus, error) {
	r.logger.V(debugLevel).Info("requesting node status", "node", r.getShortName(node))

	fqdn := r.getNodeFullyQualifiedDomainName(ts, node)
	url := fmt.Sprintf("http://%s:%d/status", fqdn, ts.Spec.ApiPort)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.logger.Error(err, "creating request failed")
		return NodeStatus{State: NotReadyState}, err
	}

	apiKey := secret.Data[ClusterAdminApiKeySecretKeyName]
	req.Header.Set("x-typesense-api-key", string(apiKey))

	resp, err := httpClient.Do(req)
	if err != nil {
		r.logger.Error(err, "executing request failed")
		return NodeStatus{State: NotReadyState}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		r.logger.Error(err, "error executing request", "httpStatusCode", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeStatus{State: NotReadyState}, err
	}

	var nodeStatus NodeStatus
	err = json.Unmarshal(body, &nodeStatus)
	if err != nil {
		return NodeStatus{State: NotReadyState}, err
	}

	r.logger.V(debugLevel).Info("reporting node status", "node", r.getShortName(node), "status", nodeStatus)
	return nodeStatus, nil
}

func (r *TypesenseClusterReconciler) getClusterStatus(quorum *Quorum, nodesStatus map[string]NodeStatus) ClusterStatus {
	leaderNodes := 0
	notReadyNodes := 0
	availableNodes := len(nodesStatus)
	minRequiredNodes := getMinimumRequiredNodes(availableNodes)

	for _, nodeStatus := range nodesStatus {
		if nodeStatus.State == LeaderState {
			leaderNodes++
		}

		if nodeStatus.State == NotReadyState {
			quorum.HealthyNodes--
			notReadyNodes++
		}
	}

	if leaderNodes > 1 {
		quorum.HealthyNodes = 0
		return ClusterStatusSplitBrain
	} else if leaderNodes == 1 {
		if minRequiredNodes < (availableNodes - notReadyNodes) {
			return ClusterStatusNotReady
		}
		return ClusterStatusOK
	}

	return ClusterStatusNotReady
}

func (r *TypesenseClusterReconciler) getNodeHealth(ctx context.Context, httpClient *http.Client, node string, ts *tsv1alpha1.TypesenseCluster) (NodeHealth, error) {
	r.logger.V(debugLevel).Info("requesting node health", "node", r.getShortName(node))

	fqdn := r.getNodeFullyQualifiedDomainName(ts, node)
	url := fmt.Sprintf("http://%s:%d/health", fqdn, ts.Spec.ApiPort)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.logger.Error(err, "creating request failed")
		return NodeHealth{Ok: false}, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		r.logger.Error(err, "executing request failed")
		return NodeHealth{Ok: false}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeHealth{Ok: false}, err
	}

	var nodeHealth NodeHealth
	err = json.Unmarshal(body, &nodeHealth)
	if err != nil {
		return NodeHealth{Ok: false}, err
	}

	r.logger.V(debugLevel).Info("reporting node health", "node", r.getShortName(node), "health", nodeHealth)
	return nodeHealth, nil
}

func (r *TypesenseClusterReconciler) getQuorum(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, sts *appsv1.StatefulSet) (*Quorum, error) {
	configMapName := fmt.Sprintf(ClusterNodesConfigMap, ts.Name)
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		r.logger.Error(err, fmt.Sprintf("unable to fetch config map: %s", configMapName))
		return &Quorum{}, err
	}

	nodes := strings.Split(cm.Data["Nodes"], ",")
	availableNodes := len(nodes)
	minRequiredNodes := getMinimumRequiredNodes(availableNodes)

	return &Quorum{minRequiredNodes, availableNodes, availableNodes, nodes, cm, sts}, nil
}

func getMinimumRequiredNodes(availableNodes int) int {
	return (availableNodes-1)/2 + 1
}
