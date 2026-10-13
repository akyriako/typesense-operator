package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TypesenseClusterReconciler) getNodeStatus(ctx context.Context, httpClient *http.Client, node NodeEndpoint, ts *tsv1alpha1.TypesenseCluster, secret *v1.Secret, logs string) (NodeStatus, error) {
	u, err := r.buildUrl(node, ts, node.ApiPort, "/status")
	if err != nil {
		return NodeStatus{State: UnreachableState}, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(ts.Spec.HealthProbeTimeoutInMilliseconds)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, u, nil)
	if err != nil {
		r.logger.Error(err, "creating request failed")
		return NodeStatus{State: ErrorState}, nil
	}

	if secret == nil || secret.Data == nil || len(secret.Data[ClusterAdminApiKeySecretKeyName]) == 0 {
		r.logger.Error(fmt.Errorf("api key missing"), "secret is missing the required data key", "ip", node.IP.String())
		return NodeStatus{State: ErrorState}, nil
	}

	apiKey := secret.Data[ClusterAdminApiKeySecretKeyName]
	req.Header.Set("x-typesense-api-key", string(apiKey))

	resp, err := httpClient.Do(req)
	if err != nil {
		r.logger.Error(err, "request failed")
		return NodeStatus{State: UnreachableState}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errStatus := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		r.logger.Error(errStatus, "error executing node status request", "httpStatusCode", resp.StatusCode, "ip", node.IP.String())
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

	if nodeStatus.State == "" {
		nodeStatus.State = ErrorState
	}

	contains := r.inspectPodLogs(logs, ErrorsRequirePodTermination...)
	if contains {
		nodeStatus.State = ErrorState
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
		if availableNodes <= 1 {
			return ClusterStatusNotReady
		} // here is setting as not ready even if the single node returns state ERROR
		if availableNodes == notReadyNodes {
			return ClusterStatusNotReady
		}
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

func (r *TypesenseClusterReconciler) getNodeHealth(ctx context.Context, httpClient *http.Client, node NodeEndpoint, ts *tsv1alpha1.TypesenseCluster, logs string) (NodeHealth, error) {
	u, err := r.buildUrl(node, ts, node.ApiPort, "/health")
	if err != nil {
		return NodeHealth{Ok: false}, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(ts.Spec.HealthProbeTimeoutInMilliseconds)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, u, nil)
	if err != nil {
		r.logger.Error(err, "creating request failed")
		return NodeHealth{Ok: false}, nil
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		r.logger.Error(err, "request failed")
		return NodeHealth{Ok: false}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeHealth{Ok: false}, nil
	}

	var nodeHealth NodeHealth
	err = json.Unmarshal(body, &nodeHealth)
	if err != nil {
		return NodeHealth{Ok: false}, nil
	}

	contains := r.inspectPodLogs(logs, ErrorsRequirePodTermination...)
	if nodeHealth.Ok && contains {
		nodeHealth.Ok = false
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

	rawNodes, ok := cm.Data["nodes"]
	if !ok || strings.TrimSpace(rawNodes) == "" {
		err := fmt.Errorf("configmap %s is missing 'nodes' key", configMapName)
		return &Quorum{}, err
	}
	nodes := strings.Split(rawNodes, ",")
	availableNodes := len(nodes)

	targetReplicas := int(ts.Spec.Replicas)
	if sts != nil && sts.Spec.Replicas != nil {
		targetReplicas = int(*sts.Spec.Replicas)
	}
	minRequiredNodes := getMinimumRequiredNodes(targetReplicas)

	var pods v1.PodList
	labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)
	if err := r.List(ctx, &pods, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		r.logger.Error(err, "failed to list pods", "statefulset", sts.Name)
		return nil, err
	}

	qn := make(map[string]NodeEndpoint)

	for _, pod := range pods.Items {
		if pod.Status.PodIP != "" {
			actualApiPort := ts.Spec.ApiPort
			for _, c := range pod.Spec.Containers {
				if c.Name == containerTypesense {
					for _, p := range c.Ports {
						if p.Name == protocolHttp {
							actualApiPort = int(p.ContainerPort)
							break
						}
					}
					break
				}
			}

			raftEndpoint := fmt.Sprintf("%s:%d:%d", pod.Status.PodIP, ts.Spec.PeeringPort, ts.Spec.ApiPort)
			dnsEndpoint := fmt.Sprintf("%s.%s:%d:%d", pod.Name, fmt.Sprintf(ClusterHeadlessService, ts.Name), ts.Spec.PeeringPort, ts.Spec.ApiPort)
			oldRaftEndpoint := fmt.Sprintf("%s:%d:%d", pod.Status.PodIP, ts.Spec.PeeringPort, actualApiPort)
			oldDnsEndpoint := fmt.Sprintf("%s.%s:%d:%d", pod.Name, fmt.Sprintf(ClusterHeadlessService, ts.Name), ts.Spec.PeeringPort, actualApiPort)
			if contains(nodes, raftEndpoint) || contains(nodes, dnsEndpoint) || contains(nodes, oldRaftEndpoint) || contains(nodes, oldDnsEndpoint) {
				qn[pod.Name] = NodeEndpoint{
					PodName: pod.Name,
					IP:      net.ParseIP(pod.Status.PodIP),
					ApiPort: actualApiPort,
				}
			}
		}
	}

	return &Quorum{minRequiredNodes, availableNodes, qn, cm}, nil
}

// getMinimumRequiredNodes returns the Raft majority (floor(n/2)+1). The StatefulSet can temporarily run
// with an even number of replicas during incremental quorum recovery, where (n-1)/2+1 would be too low.
func getMinimumRequiredNodes(availableNodes int) int {
	return availableNodes/2 + 1
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

	if cm.Data == nil {
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

func (r *TypesenseClusterReconciler) getHealthyLagThresholds(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (read int, write int) {
	read = HealthyReadLagDefaultValue
	write = HealthyWriteLagDefaultValue

	if ts.Spec.AdditionalServerConfiguration == nil {
		return read, write
	}

	configMapName := ts.Spec.AdditionalServerConfiguration.Name
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		r.logger.Error(err, "unable to additional server configuration config map", "configMap", configMapName)
		return read, write
	}

	if cm.Data == nil {
		return read, write
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
	} else {
		read = healthyReadLag
	}

	healthyWriteLag, err := strconv.Atoi(healthyWriteLagValue)
	if err != nil {
		r.logger.Error(err, "unable to parse server configuration value", "configMap", configMapName, "key", HealthyWriteLagKey)
	} else {
		write = healthyWriteLag
	}

	return read, write
}

func (r *TypesenseClusterReconciler) getHttpClient() *http.Client {
	if r.InCluster {
		return http.DefaultClient
	}

	return r.HttpClient
}

func (r *TypesenseClusterReconciler) buildUrl(node NodeEndpoint, ts *tsv1alpha1.TypesenseCluster, port int, path string) (string, error) {
	if !r.InCluster {
		req := r.ClientSet.CoreV1().RESTClient().
			Get().
			Namespace(ts.Namespace).
			Resource("pods").
			Name(fmt.Sprintf("%s:%d", r.getShortName(node.PodName), port)).
			SubResource("proxy").
			Suffix(strings.TrimPrefix(path, "/"))

		return req.URL().String(), nil
	}

	host := r.getNodeEndpoint(ts, node.IP.String())
	return url.JoinPath(fmt.Sprintf("http://%s:%d", host, port), path)
}

func (r *TypesenseClusterReconciler) getPodLogs(ctx context.Context, node NodeEndpoint, namespace string) (string, error) {
	opts := &v1.PodLogOptions{
		Container: containerTypesense,
		TailLines: ptr.To[int64](50),
		// SinceSeconds: ptr.To[int64](120),
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req := r.ClientSet.CoreV1().Pods(namespace).GetLogs(node.PodName, opts)
	raw, err := req.DoRaw(timeoutCtx)
	if err != nil {
		r.logger.Error(err, "failed to get pod logs", "pod", node.PodName, "ip", node.IP)
		return "", err
	}

	return string(raw), nil
}

func (r *TypesenseClusterReconciler) inspectPodLogs(logs string, errs ...error) bool {
	for _, e := range errs {
		if e != nil && strings.Contains(logs, e.Error()) {
			return true
		}
	}

	return false
}
