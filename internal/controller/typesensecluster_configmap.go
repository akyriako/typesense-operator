package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	forceConfigMapUpdateAnnotationKey = "ts.opentelekomcloud.com/forced-configmap-update-time"
)

func (r *TypesenseClusterReconciler) ReconcileConfigMap(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (*bool, error) {
	log.FromContext(ctx).V(debugLevel).Info("reconciling config map")

	configMapName := fmt.Sprintf(ClusterNodesConfigMap, ts.Name)
	configMapExists := true
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		if apierrors.IsNotFound(err) {
			configMapExists = false
		} else {
			log.FromContext(ctx).Error(err, fmt.Sprintf("unable to fetch config map: %s", configMapName))
			return nil, err
		}
	}

	if !configMapExists {
		log.FromContext(ctx).V(debugLevel).Info("creating config map", "configmap", configMapObjectKey.Name)

		_, err := r.createConfigMap(ctx, configMapObjectKey, ts)
		if err != nil {
			log.FromContext(ctx).Error(err, "creating config map failed", "configmap", configMapObjectKey.Name)
			return nil, err
		}

		return nil, nil
	}

	updated, err := r.updateConfigMap(ctx, ts, cm, nil, false)
	if err != nil {
		return ptr.To[bool](false), err
	}

	return &updated, nil
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

func (r *TypesenseClusterReconciler) updateConfigMap(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, cm *v1.ConfigMap, replicas *int32, resizeOp bool) (bool, error) {
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
				return false, err
			}
		} else {
			log.FromContext(ctx).Error(err, fmt.Sprintf("unable to fetch statefulset: %s", stsName))
		}

		return false, err
	}

	if replicas == nil {
		if sts.Spec.Replicas != nil {
			replicas = sts.Spec.Replicas
		} else {
			replicas = &ts.Spec.Replicas
		}
	}

	nodes, err := r.getNodes(ctx, ts, *replicas, resizeOp)
	if err != nil {
		return false, err
	}
	fallback, err := r.getNodes(ctx, ts, *replicas, true)
	if err != nil {
		return false, err
	}

	availableNodes := len(nodes)
	if availableNodes < int(*replicas) {
		log.FromContext(ctx).V(debugLevel).Info("incomplete quorum configuration, using fallback nodes")
		nodes = fallback
		availableNodes = len(nodes)
	}

	nodesStr := strings.Join(nodes, ",")
	fallbackStr := strings.Join(fallback, ",")

	if !resizeOp {
		currentNodes := strings.Split(cm.Data["nodes"], ",")
		log.FromContext(ctx).V(debugLevel).Info("current quorum configuration", "size", len(currentNodes), "nodes", currentNodes)
	}

	updated := false
	if cm.Data["nodes"] != nodesStr || cm.Data["fallback"] != fallbackStr {
		if !resizeOp {
			log.FromContext(ctx).Info("updating quorum configuration", "size", availableNodes, "nodes", nodes)
		}

		desired := &v1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: v1.SchemeGroupVersion.String(),
				Kind:       "ConfigMap",
			},
			ObjectMeta: getObjectMeta(ts, &cm.Name, nil),
			Data: map[string]string{
				"nodes":    nodesStr,
				"fallback": fallbackStr,
			},
		}

		//nolint:staticcheck
		err := r.Patch(ctx, desired, client.Apply, client.ForceOwnership, client.FieldOwner("typesense-operator"))
		if err != nil {
			log.FromContext(ctx).Error(err, "updating quorum configuration failed")
			return false, err
		}
		updated = true
	}

	return updated, nil
}

func (r *TypesenseClusterReconciler) deleteConfigMap(ctx context.Context, cm *v1.ConfigMap) error {
	err := r.Delete(ctx, cm)
	return client.IgnoreNotFound(err)
}

// ForcePodsConfigMapUpdate forces a configmap update for all pods in the statefulset
// it should be called after a configmap update occurs
// https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/#mounted-configmaps-are-updated-automatically
func (r *TypesenseClusterReconciler) forcePodsConfigMapUpdate(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	labelMap := getLabels(ts)
	labelSelector := labels.SelectorFromSet(labelMap)

	var podList v1.PodList
	if err := r.List(ctx, &podList,
		client.InNamespace(ts.Namespace),
		client.MatchingLabelsSelector{Selector: labelSelector},
	); err != nil {
		return err
	}

	var errs []error
	for i := range podList.Items {
		pod := &podList.Items[i]
		original := pod.DeepCopy()

		if !pod.DeletionTimestamp.IsZero() {
			continue
		}

		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[forceConfigMapUpdateAnnotationKey] = time.Now().Format(time.RFC3339)

		if err := r.Patch(ctx, pod, client.MergeFrom(original)); err != nil {
			log.FromContext(ctx).Error(err, "patching pod annotations failed", "pod", pod.Name)
			errs = append(errs, fmt.Errorf("pod %s: %w", pod.Name, err))
			continue
		}

		log.FromContext(ctx).V(debugLevel).Info("patching pod annotations", "pod", pod.Name, "annotation", forceConfigMapUpdateAnnotationKey)
	}

	return utilerrors.NewAggregate(errs)
}

func (r *TypesenseClusterReconciler) getNodes(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, replicas int32, bootstrapping bool) ([]string, error) {
	nodes := make([]string, 0)

	podApiPortsByIP := make(map[string]int)
	podApiPortsByName := make(map[string]int)
	var pods v1.PodList

	stsExists := true
	stsName := fmt.Sprintf(ClusterStatefulSet, ts.Name)
	stsObjectKey := client.ObjectKey{
		Name:      stsName,
		Namespace: ts.Namespace,
	}
	sts, err := r.GetFreshStatefulSet(ctx, stsObjectKey)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		stsExists = false
	}

	if stsExists {
		labelSelector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)

		if err := r.List(ctx, &pods, &client.ListOptions{
			Namespace:     sts.Namespace,
			LabelSelector: labelSelector,
		}); err != nil {
			log.FromContext(ctx).Error(err, "failed to list pods", "statefulset", sts.Name)
		} else {
			for _, pod := range pods.Items {
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
				if pod.Status.PodIP != "" {
					podApiPortsByIP[pod.Status.PodIP] = actualApiPort
				}
				podApiPortsByName[pod.Name] = actualApiPort
			}
		}
	}

	getFallbackNodes := func(nodes []string) ([]string, error) {
		for i := 0; i < int(replicas); i++ {
			nodeName := fmt.Sprintf("%s-sts-%d.%s-sts-svc", ts.Name, i, ts.Name)
			podName := fmt.Sprintf("%s-sts-%d", ts.Name, i)

			apiPort := ts.Spec.ApiPort
			if p, ok := podApiPortsByName[podName]; ok {
				apiPort = p
			}

			if len(nodeName) > nodeNameLenLimit {
				return nil, fmt.Errorf("raft error: node name should not exceed %d characters: %s", nodeNameLenLimit, nodeName)
			}

			nodes = append(nodes, fmt.Sprintf("%s:%d:%d", nodeName, ts.Spec.PeeringPort, apiPort))
		}

		return nodes, nil
	}

	if bootstrapping {
		fallbackNodes := make([]string, 0)
		return getFallbackNodes(fallbackNodes)
	}

	unscheduledPods := int32(0)
	targetReplicas := replicas

	if !stsExists {
		unscheduledPods = replicas
	} else {

		if len(pods.Items) == 0 {
			fallbackNodes := make([]string, 0)
			return getFallbackNodes(fallbackNodes)
		}

		for _, pod := range pods.Items {
			markAsScheduled := false
			// mark the pod as unscheduled if it's still pulling the image
		CheckContainers:
			for _, cs := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
				if cs.State.Waiting == nil {
					continue
				}

				switch cs.State.Waiting.Reason {
				case "ContainerCreating", "ErrImagePull", "ImagePullBackOff":
					markAsScheduled = true
					break CheckContainers
				}
			}

			if isPodUnschedulable(&pod) {
				markAsScheduled = true
			}

			if markAsScheduled {
				unscheduledPods++
			}
		}

		targetReplicas = ptr.Deref[int32](sts.Spec.Replicas, 1)
	}

	if unscheduledPods == targetReplicas {
		fallbackNodes := make([]string, 0)
		return getFallbackNodes(fallbackNodes)
	}

	eps, err := r.getEndpointSlicesForStatefulSet(ctx, sts)
	if err != nil {
		return nil, err
	}

	// The same endpoint may temporarily appear in more than one slice (e.g. while slices are rebalanced).
	// Duplicates would let an incomplete peer list pass the replica count check.
	seen := make(map[string]struct{})
	for _, s := range eps {
		for _, e := range s.Endpoints {
			if len(e.Addresses) > 0 {
				addr := e.Addresses[0]
				if _, ok := seen[addr]; ok {
					continue
				}
				seen[addr] = struct{}{}

				apiPort := ts.Spec.ApiPort
				if p, ok := podApiPortsByIP[addr]; ok {
					apiPort = p
				}

				nodes = append(nodes, fmt.Sprintf("%s:%d:%d", addr, ts.Spec.PeeringPort, apiPort))
			}
		}
	}

	sort.Strings(nodes)

	return nodes, nil
}

func (r *TypesenseClusterReconciler) getEndpointSlicesForStatefulSet(ctx context.Context, sts *appsv1.StatefulSet) ([]discoveryv1.EndpointSlice, error) {
	log.FromContext(ctx).V(debugLevel).Info("collecting endpoint slices")
	svcName := sts.Spec.ServiceName
	namespace := sts.Namespace

	// 1) List EndpointSlices for headless Service
	var sliceList discoveryv1.EndpointSliceList
	if err := r.List(ctx, &sliceList,
		client.InNamespace(namespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: svcName},
	); err != nil {
		return nil, err
	}

	// 2) Build a set of “live” Pod IPs for this StatefulSet
	selector := labels.SelectorFromSet(sts.Spec.Selector.MatchLabels)
	var podList v1.PodList
	if err := r.List(ctx, &podList,
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
		var readyEndpoints []discoveryv1.Endpoint
		for _, ep := range slice.Endpoints {
			// only consider endpoints that reference a Pod and whose IP is still live
			if ep.TargetRef != nil &&
				ep.TargetRef.Kind == "Pod" &&
				len(ep.Addresses) > 0 {
				ip := ep.Addresses[0]
				if _, ok := liveIPs[ip]; ok {
					readyEndpoints = append(readyEndpoints, ep)
				}
			}
		}
		if len(readyEndpoints) > 0 {
			slice.Endpoints = readyEndpoints
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
