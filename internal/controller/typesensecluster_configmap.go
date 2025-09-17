package controller

import (
	"context"
	"fmt"
	"strings"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TypesenseClusterReconciler) ReconcileConfigMap(ctx context.Context, ts tsv1alpha1.TypesenseCluster) (bool, error) {
	r.logger.V(debugLevel).Info("reconciling config map")

	configMapName := fmt.Sprintf(ClusterNodesConfigMap, ts.Name)
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	configMapExists := true
	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		if apierrors.IsNotFound(err) {
			configMapExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch config map: %s", configMapName))
			return false, err
		}
	}

	var err error
	updated := false
	if !configMapExists {
		r.logger.V(debugLevel).Info("creating config map", "configmap", configMapObjectKey.Name)

		_, err := r.createConfigMap(ctx, configMapObjectKey, &ts)
		if err != nil {
			r.logger.Error(err, "creating config map failed", "configmap", configMapObjectKey.Name)
			return false, err
		}
	} else {
		r.logger.V(debugLevel).Info("updating config map", "configmap", configMapObjectKey.Name)

		_, _, updated, err = r.updateConfigMap(ctx, &ts, cm, nil)
		if err != nil {
			return false, err
		}
	}

	return updated, nil
}

const nodeNameLenLimit = 64

func (r *TypesenseClusterReconciler) createConfigMap(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*v1.ConfigMap, error) {
	nodes, err := r.getNodes(ctx, ts, ts.Spec.Replicas, true)
	if err != nil {
		return nil, err
	}

	cm := &v1.ConfigMap{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Data: map[string]string{
			"nodes":    strings.Join(nodes, ","),
			"fallback": strings.Join(nodes, ","),
		},
	}

	err = ctrl.SetControllerReference(ts, cm, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, cm)
	if err != nil {
		return nil, err
	}

	return cm, nil
}

func (r *TypesenseClusterReconciler) updateConfigMap(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, cm *v1.ConfigMap, replicas *int32) (*v1.ConfigMap, int, bool, error) {
	stsName := fmt.Sprintf(ClusterStatefulSet, ts.Name)
	stsObjectKey := client.ObjectKey{
		Name:      stsName,
		Namespace: ts.Namespace,
	}

	var sts = &appsv1.StatefulSet{}
	if err := r.Get(ctx, stsObjectKey, sts); err != nil {
		if apierrors.IsNotFound(err) {
			err := r.deleteConfigMap(ctx, cm)
			if err != nil {
				return nil, 0, false, err
			}
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch statefulset: %s", stsName))
		}

		return nil, 0, false, err
	}

	if replicas == nil {
		replicas = sts.Spec.Replicas
	}

	nodes, err := r.getNodes(ctx, ts, *replicas, false)
	if err != nil {
		return nil, 0, false, err
	}
	fallback, err := r.getNodes(ctx, ts, *replicas, true)
	if err != nil {
		return nil, 0, false, err
	}

	availableNodes := len(nodes)
	if availableNodes == 0 {
		r.logger.V(debugLevel).Info("empty quorum configuration")
		return nil, 0, false, fmt.Errorf("empty quorum configuration")
	}

	desired := cm.DeepCopy()
	desired.Data = map[string]string{
		"nodes":    strings.Join(nodes, ","),
		"fallback": strings.Join(fallback, ","),
	}

	r.logger.V(debugLevel).Info("current quorum configuration", "size", availableNodes, "nodes", nodes)

	updated := false
	if cm.Data["nodes"] != desired.Data["nodes"] || cm.Data["fallback"] != desired.Data["fallback"] {
		r.logger.Info("updating quorum configuration", "size", availableNodes, "nodes", nodes)

		err := r.Update(ctx, desired)
		if err != nil {
			r.logger.Error(err, "updating quorum configuration failed")
			return nil, 0, false, err
		}
		updated = true
	}

	return desired, availableNodes, updated, nil
}

func (r *TypesenseClusterReconciler) deleteConfigMap(ctx context.Context, cm *v1.ConfigMap) error {
	err := r.Delete(ctx, cm)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) getNodes(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, replicas int32, bootstrapping bool) ([]string, error) {
	nodes := make([]string, 0)

	if bootstrapping {
		for i := 0; i < int(replicas); i++ {
			nodeName := fmt.Sprintf("%s-sts-%d.%s-sts-svc", ts.Name, i, ts.Name)
			if len(nodeName) > nodeNameLenLimit {
				return nil, fmt.Errorf("raft error: node name should not exceed %d characters: %s", nodeNameLenLimit, nodeName)
			}

			nodes = append(nodes, fmt.Sprintf("%s:%d:%d", nodeName, ts.Spec.PeeringPort, ts.Spec.ApiPort))
		}

		return nodes, nil
	}

	stsName := fmt.Sprintf(ClusterStatefulSet, ts.Name)
	stsObjectKey := client.ObjectKey{
		Name:      stsName,
		Namespace: ts.Namespace,
	}
	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		return nil, err
	}

	slices, err := r.getEndpointSlicesForStatefulSet(ctx, sts)
	if err != nil {
		return nil, err
	}

	i := 0
	for _, s := range slices {
		for _, e := range s.Endpoints {
			addr := e.Addresses[0]
			//r.logger.V(debugLevel).Info("discovered slice endpoint", "slice", s.Name, "endpoint", e.Hostname, "address", addr)
			nodes = append(nodes, fmt.Sprintf("%s:%d:%d", addr, ts.Spec.PeeringPort, ts.Spec.ApiPort))

			i++
		}
	}

	return nodes, nil
}

func (r *TypesenseClusterReconciler) getEndpointSlicesForStatefulSet(ctx context.Context, sts *appsv1.StatefulSet) ([]discoveryv1.EndpointSlice, error) {
	r.logger.V(debugLevel).Info("collecting endpoint slices")
	svcName := sts.Spec.ServiceName
	namespace := sts.Namespace

	// 1) List EndpointSlices for headless Service
	var sliceList discoveryv1.EndpointSliceList
	if err := r.Client.List(ctx, &sliceList,
		client.InNamespace(namespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: svcName},
	); err != nil {
		return nil, err
	}

	// 2) Build a set of “live” Pod IPs for this StatefulSet
	selector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)
	var podList v1.PodList
	if err := r.Client.List(ctx, &podList,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, err
	}
	liveIPs := map[string]struct{}{}
	for _, pod := range podList.Items {
		if pod.DeletionTimestamp == nil && (pod.Status.Phase == v1.PodRunning || pod.Status.Phase == v1.PodPending) && pod.Status.PodIP != "" {
			liveIPs[pod.Status.PodIP] = struct{}{}
		}
	}

	// 3) Filter slices: keep only slices that contain at least one endpoint
	//    whose IP is still in liveIPs
	var readySlices []discoveryv1.EndpointSlice
	for _, slice := range sliceList.Items {
		keep := false
		for _, ep := range slice.Endpoints {
			// only consider endpoints that reference a Pod and whose IP is still live
			if ep.TargetRef != nil &&
				ep.TargetRef.Kind == "Pod" &&
				len(ep.Addresses) > 0 {
				ip := ep.Addresses[0]
				if _, ok := liveIPs[ip]; ok {
					keep = true
					break
				}
			}
		}
		if keep {
			readySlices = append(readySlices, slice)
		}
	}

	return readySlices, nil
}

func (r *TypesenseClusterReconciler) getNodeEndpoint(ts *tsv1alpha1.TypesenseCluster, raftNodeEndpoint string) string {
	if hasIP4Prefix(raftNodeEndpoint) {
		node := strings.Replace(raftNodeEndpoint, fmt.Sprintf(":%d:%d", ts.Spec.PeeringPort, ts.Spec.ApiPort), "", 1)
		return node
	}

	node := strings.Replace(raftNodeEndpoint, fmt.Sprintf(":%d:%d", ts.Spec.PeeringPort, ts.Spec.ApiPort), "", 1)
	fqdn := fmt.Sprintf("%s.%s-sts-svc.%s.svc.cluster.local", node, ts.Name, ts.Namespace)

	return fqdn
}

func (r *TypesenseClusterReconciler) getShortName(raftNodeEndpoint string) string {
	parts := strings.SplitN(raftNodeEndpoint, ":", 2)
	host := parts[0]

	if hasIP4Prefix(host) {
		return host
	}

	if idx := strings.Index(host, "."); idx != -1 {
		return host[:idx]
	}

	return host
}
