package controller

import (
	"context"
	"encoding/json"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"io"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"net"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
)

func (r *TypesenseClusterReconciler) getNodeStatus(ctx context.Context, httpClient *http.Client, node NodeEndpoint, ts *tsv1alpha1.TypesenseCluster, secret *v1.Secret) (NodeStatus, error) {
	fqdn := r.getNodeEndpoint(ts, node.IP.String())
	url := fmt.Sprintf("http://%s:%d/status", fqdn, ts.Spec.ApiPort)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.logger.Error(err, "creating request failed")
		return NodeStatus{State: ErrorState}, nil
	}

	apiKey := secret.Data[ClusterAdminApiKeySecretKeyName]
	req.Header.Set("x-typesense-api-key", string(apiKey))

	resp, err := httpClient.Do(req)
	if err != nil {
		r.logger.Error(err, "request failed")
		return NodeStatus{State: UnreachableState}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		r.logger.Error(err, "error executing request", "httpStatusCode", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeStatus{State: ErrorState}, nil
	}

	var nodeStatus NodeStatus
	err = json.Unmarshal(body, &nodeStatus)
	if err != nil {
		return NodeStatus{State: ErrorState}, nil
	}

	return nodeStatus, nil
}

func (r *TypesenseClusterReconciler) getClusterStatus(nodesStatus map[string]NodeStatus) ClusterStatus {
	leaderNodes := 0
	notReadyNodes := 0
	availableNodes := len(nodesStatus)
	minRequiredNodes := getMinimumRequiredNodes(availableNodes)

	for _, nodeStatus := range nodesStatus {
		if nodeStatus.State == LeaderState {
			leaderNodes++
		}

		if nodeStatus.State == NotReadyState || nodeStatus.State == UnreachableState {
			notReadyNodes++
		}
	}

	if leaderNodes > 1 {
		return ClusterStatusSplitBrain
	}

	if leaderNodes == 0 {
		if availableNodes == 1 {
			return ClusterStatusNotReady
		} // here is setting as not ready even if the single node returns state ERROR
		return ClusterStatusElectionDeadlock
	}

	if leaderNodes == 1 {
		if minRequiredNodes > (availableNodes - notReadyNodes) {
			return ClusterStatusNotReady
		}
		return ClusterStatusOK
	}

	return ClusterStatusNotReady
}

func (r *TypesenseClusterReconciler) getNodeHealth(ctx context.Context, httpClient *http.Client, node NodeEndpoint, ts *tsv1alpha1.TypesenseCluster) (NodeHealth, error) {
	fqdn := r.getNodeEndpoint(ts, node.IP.String())
	url := fmt.Sprintf("http://%s:%d/health", fqdn, ts.Spec.ApiPort)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.logger.Error(err, "creating request failed")
		return NodeHealth{Ok: false}, nil
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		r.logger.Error(err, "request failed")
		return NodeHealth{Ok: false}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeHealth{Ok: false}, nil
	}

	var nodeHealth NodeHealth
	err = json.Unmarshal(body, &nodeHealth)
	if err != nil {
		return NodeHealth{Ok: false}, nil
	}

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

	nodes := strings.Split(cm.Data["nodes"], ",")
	availableNodes := len(nodes)
	minRequiredNodes := getMinimumRequiredNodes(availableNodes)

	var pods v1.PodList
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)
	if err := r.List(ctx, &pods, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		r.logger.Error(err, "failed to list pods", "statefulset", sts.Name)
		return nil, err
	}

	qn := make(map[string]net.IP)

	for _, pod := range pods.Items {
		if pod.Status.PodIP != "" {
			raftEndpoint := fmt.Sprintf("%s:%d:%d", pod.Status.PodIP, ts.Spec.PeeringPort, ts.Spec.ApiPort)
			if _, contains := contains(nodes, raftEndpoint); contains {
				qn[pod.Name] = net.ParseIP(pod.Status.PodIP)
			}
		}
	}

	return &Quorum{minRequiredNodes, availableNodes, qn, cm}, nil
}

func getMinimumRequiredNodes(availableNodes int) int {
	return (availableNodes-1)/2 + 1
}

func (r *TypesenseClusterReconciler) getHealthyWriteLagThreshold(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) int {
	if ts.Spec.AdditionalServerConfiguration == nil {
		return HealthyWriteLagDefaultValue
	}

	configMapName := ts.Spec.AdditionalServerConfiguration.Name
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		r.logger.Error(err, "unable to additional server configuration config map", "configMap", configMapName)
		return HealthyWriteLagDefaultValue
	}

	healthyWriteLagValue := cm.Data[HealthyWriteLagKey]
	if healthyWriteLagValue == "" {
		return HealthyWriteLagDefaultValue
	}

	healthyWriteLag, err := strconv.Atoi(healthyWriteLagValue)
	if err != nil {
		r.logger.Error(err, "unable to parse server configuration value", "configMap", configMapName, "key", HealthyWriteLagKey)
		return HealthyWriteLagDefaultValue
	}

	return healthyWriteLag
}

func (r *TypesenseClusterReconciler) getHealthyReadLagThreshold(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) int {
	if ts.Spec.AdditionalServerConfiguration == nil {
		return HealthyReadLagDefaultValue
	}

	configMapName := ts.Spec.AdditionalServerConfiguration.Name
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		r.logger.Error(err, "unable to additional server configuration config map", "configMap", configMapName)
		return HealthyReadLagDefaultValue
	}

	healthyReadLagValue := cm.Data[HealthyReadLagKey]
	if healthyReadLagValue == "" {
		return HealthyReadLagDefaultValue
	}

	healthyReadLag, err := strconv.Atoi(healthyReadLagValue)
	if err != nil {
		r.logger.Error(err, "unable to parse server configuration value", "configMap", configMapName, "key", HealthyReadLagKey)
		return HealthyReadLagDefaultValue
	}

	return healthyReadLag
}

func (r *TypesenseClusterReconciler) getHealthyLagThresholds(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (read int, write int) {
	read = HealthyReadLagDefaultValue
	write = HealthyWriteLagDefaultValue

	if ts.Spec.AdditionalServerConfiguration == nil {
		return
	}

	configMapName := ts.Spec.AdditionalServerConfiguration.Name
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		r.logger.Error(err, "unable to additional server configuration config map", "configMap", configMapName)
		return
	}

	healthyReadLagValue := cm.Data[HealthyReadLagKey]
	if healthyReadLagValue == "" {
		healthyReadLagValue = strconv.Itoa(HealthyReadLagDefaultValue)
	}

	healthyWriteLagValue := cm.Data[HealthyWriteLagKey]
	if healthyWriteLagValue == "" {
		healthyWriteLagValue = strconv.Itoa(HealthyWriteLagDefaultValue)
	}

	healthyReadLag, err := strconv.Atoi(healthyReadLagValue)
	if err != nil {
		r.logger.Error(err, "unable to parse server configuration value", "configMap", configMapName, "key", HealthyReadLagKey)
	}

	healthyWriteLag, err := strconv.Atoi(healthyWriteLagValue)
	if err != nil {
		r.logger.Error(err, "unable to parse server configuration value", "configMap", configMapName, "key", HealthyWriteLagKey)
	}

	read = healthyReadLag
	write = healthyWriteLag

	return
}
