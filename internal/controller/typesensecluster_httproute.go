package controller

import (
	"context"
	"fmt"
	"maps"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

const (
	minimumSupportedVersionForGateway = "1.26.0"
	gatewayApiGroup                   = "gateway.networking.k8s.io"
	kindHTTPRoute                     = "HTTPRoute"
)

//nolint:gocyclo // ReconcileHttpRoute sequentially processes HTTP routes
func (r *TypesenseClusterReconciler) ReconcileHttpRoute(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (err error) {
	if supported, ver, err := r.IsFeatureSupported(minimumSupportedVersionForGateway); !supported || err != nil {
		if err != nil {
			return err
		}

		notSupportedErr := fmt.Errorf("gateway is not supported in kubernetes current version")
		r.logger.Error(notSupportedErr, "reconciling http routes skipped", "current", ver, "minimum_required", fmt.Sprintf("v%s", minimumSupportedVersionForGateway))
		return nil
	}

	if deployed, err := r.IsApiGroupDeployed(gatewayApiGroup); err != nil || !deployed {
		if len(ts.Spec.HttpRoutes) > 0 {
			err := fmt.Errorf("gateway api group %s was not found in cluster", gatewayApiGroup)
			r.logger.Error(err, "reconciling http routes skipped")
		}
		return nil
	}

	r.logger.V(debugLevel).Info("reconciling http routes")

	err = r.deleteOrphanedHttpRoutes(ctx, ts)
	if err != nil {
		return err
	}

	for _, hrt := range ts.Spec.HttpRoutes {
		httpRouteName := fmt.Sprintf(ClusterHttpRoute, ts.Name, hrt.Name)
		httpRouteExists := true
		httpRouteObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: httpRouteName}

		var httpRoute = &gatewayv1.HTTPRoute{}
		if err := r.Get(ctx, httpRouteObjectKey, httpRoute); err != nil {
			if apierrors.IsNotFound(err) {
				httpRouteExists = false
			} else {
				r.logger.Error(err, fmt.Sprintf("unable to fetch http route: %s", httpRouteName))
				return err
			}
		}

		if !httpRouteExists && hrt.Enabled {
			r.logger.V(debugLevel).Info("creating http route", "http_route", httpRouteName)

			_, err = r.createHttpRoute(ctx, httpRouteObjectKey, hrt, ts)
			if err != nil {
				r.logger.Error(err, "creating http route failed", "http_route", httpRouteName)
				return err
			}

			if ptr.Deref(hrt.ReferenceGrant, false) {
				err := r.createReferenceGrant(ctx, hrt, ts)
				if err != nil {
					r.logger.Error(err, "creating reference grant failed", "http_route", httpRouteName)
					return err
				}
			}
		} else {
			if !hrt.Enabled {
				referenceGrantsLabelSelector := labels.SelectorFromSet(map[string]string{
					"route": httpRoute.Name,
				})

				var referenceGrants gatewayv1beta1.ReferenceGrantList
				if err := r.List(ctx, &referenceGrants, &client.ListOptions{
					LabelSelector: referenceGrantsLabelSelector,
				}); err != nil {
					gerr := fmt.Errorf("failed to list reference grants: %w", err)
					r.logger.Error(gerr, "reconciling http routes failed")
					return gerr
				}

				for _, rg := range referenceGrants.Items {
					if !isReferenceGrantOwnedBy(&rg, ts) {
						continue
					}

					grant := rg
					err := r.deleteReferenceGrant(ctx, &grant)
					if err != nil {
						if !apierrors.IsNotFound(err) {
							r.logger.Error(err, "deleting reference grant failed: %w", err)
						}
					}
				}

				err = r.deleteHttpRoute(ctx, httpRoute)
				if err != nil {
					gerr := fmt.Errorf("deleting http route failed: %w", err)
					r.logger.Error(gerr, "reconciling http routes failed")
					return gerr
				}
				continue
			}

			lbls := r.getHttpRouteLabels(httpRoute, hrt, ts)
			annotations := r.getHttpRouteAnnotations(httpRoute, ts)

			pRef := hrt.ParentRef
			kind := gatewayv1.Kind("Gateway")
			group := gatewayv1.Group(gatewayApiGroup)
			parentRef := gatewayv1.ParentReference{
				Group:       &group,
				Kind:        &kind,
				Name:        gatewayv1.ObjectName(pRef.Name),
				Namespace:   pRef.Namespace,
				SectionName: pRef.SectionName,
			}

			hostnames := make([]gatewayv1.Hostname, 0, len(hrt.Hostnames))
			for _, h := range hrt.Hostnames {
				hostnames = append(hostnames, gatewayv1.Hostname(h))
			}

			var path string
			var pathType *gatewayv1.PathMatchType

			hasValidMatch := len(httpRoute.Spec.Rules) > 0 &&
				len(httpRoute.Spec.Rules[0].Matches) > 0 &&
				httpRoute.Spec.Rules[0].Matches[0].Path != nil

			if hasValidMatch {
				if httpRoute.Spec.Rules[0].Matches[0].Path.Value != nil {
					path = *httpRoute.Spec.Rules[0].Matches[0].Path.Value
				}
				pathType = httpRoute.Spec.Rules[0].Matches[0].Path.Type
			}

			expectedPathType := hrt.PathType
			if expectedPathType == nil {
				expectedPathType = ptr.To(gatewayv1.PathMatchPathPrefix)
			}

			var currentBackendPort *gatewayv1.PortNumber
			if hasValidMatch && len(httpRoute.Spec.Rules[0].BackendRefs) > 0 && httpRoute.Spec.Rules[0].BackendRefs[0].Port != nil {
				currentBackendPort = httpRoute.Spec.Rules[0].BackendRefs[0].Port
			}
			expectedBackendPort := gatewayv1.PortNumber(ts.Spec.ApiPort)

			if !apiequality.Semantic.DeepEqual(hostnames, httpRoute.Spec.Hostnames) ||
				!apiequality.Semantic.DeepEqual(hrt.Labels, lbls) ||
				!apiequality.Semantic.DeepEqual(hrt.Annotations, annotations) ||
				len(httpRoute.Spec.ParentRefs) == 0 || !apiequality.Semantic.DeepEqual(parentRef, httpRoute.Spec.ParentRefs[0]) ||
				!hasValidMatch || hrt.Path != path || pathType == nil || *expectedPathType != *pathType ||
				currentBackendPort == nil || *currentBackendPort != expectedBackendPort {

				r.logger.V(debugLevel).Info("updating http route", "http_route", httpRouteName)

				_, err = r.updateHttpRoute(ctx, hrt, httpRoute, ts)
				if err != nil {
					r.logger.Error(err, "updating http route failed", "http_route", httpRouteName)
					return err
				}
			}

			err := r.updateReferenceGrant(ctx, hrt, ts)
			if err != nil {
				return err
			}
		}
	}

	// Runs after the desired grants have been ensured, so grants still carrying the legacy
	// (namespace-less) name are only removed once their replacement exists.
	return r.deleteOrphanedReferenceGrants(ctx, ts)
}

func (r *TypesenseClusterReconciler) createHttpRoute(ctx context.Context, key client.ObjectKey, spec tsv1alpha1.HttpRouteSpec, ts *tsv1alpha1.TypesenseCluster) (*gatewayv1.HTTPRoute, error) {
	annotations := map[string]string{}
	if spec.Annotations != nil {
		maps.Copy(annotations, spec.Annotations)
	}

	lbls := map[string]string{}
	if spec.Labels != nil {
		maps.Copy(lbls, spec.Labels)
	}

	parentRef := r.getGatewayParentRef(spec, ts)

	hostnames := make([]gatewayv1.Hostname, 0, len(spec.Hostnames))
	for _, h := range spec.Hostnames {
		hostnames = append(hostnames, gatewayv1.Hostname(h))
	}

	expectedPathType := spec.PathType
	if expectedPathType == nil {
		expectedPathType = ptr.To(gatewayv1.PathMatchPathPrefix)
	}

	backendPort := gatewayv1.PortNumber(ts.Spec.ApiPort)
	backendNamespace := gatewayv1.Namespace(ts.Namespace)
	backendRef := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Group:     ptr.To(gatewayv1.Group("")),
				Kind:      ptr.To(gatewayv1.Kind("Service")),
				Name:      gatewayv1.ObjectName(fmt.Sprintf(ClusterRestService, ts.Name)),
				Namespace: &backendNamespace,
				Port:      &backendPort,
			},
			Weight: ptr.To(int32(1)),
		},
	}

	httpRoute := &gatewayv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gatewayv1.GroupVersion.String(),
			Kind:       kindHTTPRoute,
		},
		ObjectMeta: getHttpRouteObjectMeta(ts, spec, &key.Name, lbls, annotations),
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parentRef},
			},
			Hostnames: hostnames,
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  expectedPathType,
								Value: &spec.Path,
							},
						},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{backendRef},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, httpRoute, r.Scheme)
	if err != nil {
		return nil, err
	}

	//nolint:staticcheck
	err = r.Patch(ctx, httpRoute, client.Apply, client.ForceOwnership, client.FieldOwner(operatorFieldOwner))
	if err != nil {
		return nil, err
	}

	return httpRoute, nil
}

func (r *TypesenseClusterReconciler) deleteHttpRoute(ctx context.Context, httpRoute *gatewayv1.HTTPRoute) error {
	err := r.Delete(ctx, httpRoute)
	return client.IgnoreNotFound(err)
}

func (r *TypesenseClusterReconciler) deleteOrphanedHttpRoutes(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	httpRouteLabelSelector := labels.SelectorFromSet(getLabels(ts))
	var httpRoutes gatewayv1.HTTPRouteList
	if err := r.List(ctx, &httpRoutes, &client.ListOptions{
		Namespace:     ts.Namespace,
		LabelSelector: httpRouteLabelSelector,
	}); err != nil {
		gerr := fmt.Errorf("failed to list http routes: %w", err)
		r.logger.Error(gerr, "reconciling http routes skipped")
		return gerr
	}

	// Delete HTTPRoutes that still in action but not anymore in new specs
	for _, eroute := range httpRoutes.Items {
		exists := false
		for _, droute := range ts.Spec.HttpRoutes {
			drouteName := fmt.Sprintf(ClusterHttpRoute, ts.Name, droute.Name)
			if eroute.Name == drouteName {
				exists = true
				break
			}
		}

		if !exists {
			route := eroute
			err := r.deleteHttpRoute(ctx, &route)
			if err != nil {
				gerr := fmt.Errorf("deleting http route failed: %w", err)
				r.logger.Error(gerr, "reconciling http routes failed")
				return gerr
			}
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) updateHttpRoute(ctx context.Context, spec tsv1alpha1.HttpRouteSpec, httpRoute *gatewayv1.HTTPRoute, ts *tsv1alpha1.TypesenseCluster) (*gatewayv1.HTTPRoute, error) {
	if err := r.migrateLegacyFieldOwnership(ctx, httpRoute); err != nil {
		return nil, err
	}

	parentRef := r.getGatewayParentRef(spec, ts)

	hostnames := make([]gatewayv1.Hostname, 0, len(spec.Hostnames))
	for _, h := range spec.Hostnames {
		hostnames = append(hostnames, gatewayv1.Hostname(h))
	}

	expectedPathType := spec.PathType
	if expectedPathType == nil {
		expectedPathType = ptr.To(gatewayv1.PathMatchPathPrefix)
	}

	backendPort := gatewayv1.PortNumber(ts.Spec.ApiPort)
	backendNamespace := gatewayv1.Namespace(ts.Namespace)
	backendRef := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Group:     ptr.To(gatewayv1.Group("")),
				Kind:      ptr.To(gatewayv1.Kind("Service")),
				Name:      gatewayv1.ObjectName(fmt.Sprintf(ClusterRestService, ts.Name)),
				Namespace: &backendNamespace,
				Port:      &backendPort,
			},
			Weight: ptr.To(int32(1)),
		},
	}

	lbls := map[string]string{}
	if spec.Labels != nil {
		maps.Copy(lbls, spec.Labels)
	}

	annotations := map[string]string{}
	if spec.Annotations != nil {
		maps.Copy(annotations, spec.Annotations)
	}

	desired := &gatewayv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gatewayv1.GroupVersion.String(),
			Kind:       kindHTTPRoute,
		},
		ObjectMeta: getHttpRouteObjectMeta(ts, spec, &httpRoute.Name, lbls, annotations),
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{parentRef},
			},
			Hostnames: hostnames,
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  expectedPathType,
								Value: &spec.Path,
							},
						},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{backendRef},
				},
			},
		},
	}

	// The owner reference must be part of the applied configuration, otherwise server-side apply
	// removes it once the operator owns it.
	if err := ctrl.SetControllerReference(ts, desired, r.Scheme); err != nil {
		return nil, err
	}

	//nolint:staticcheck
	if err := r.Patch(ctx, desired, client.Apply, client.ForceOwnership, client.FieldOwner(operatorFieldOwner)); err != nil {
		return nil, err
	}

	return desired, nil
}

func (r *TypesenseClusterReconciler) getHttpRouteAnnotations(httpRoute *gatewayv1.HTTPRoute, ts *tsv1alpha1.TypesenseCluster) map[string]string {
	filters := append([]string{clusterIssuerAnnotationKey, rancherDomainAnnotationKey}, ts.Spec.IgnoreAnnotationsFromExternalMutations...)
	filtered := filterMap(httpRoute.Annotations, filters...)

	return filtered
}

func (r *TypesenseClusterReconciler) getHttpRouteLabels(httpRoute *gatewayv1.HTTPRoute, spec tsv1alpha1.HttpRouteSpec, ts *tsv1alpha1.TypesenseCluster) map[string]string {
	filters := make([]string, 0)
	lbls := getHttpRouteLabels(ts, spec)
	for k := range maps.Keys(lbls) {
		filters = append(filters, k)
	}

	filtered := withoutKeys(httpRoute.Labels, filters...)

	if len(filtered) == 0 {
		return nil
	}

	return filtered
}

func (r *TypesenseClusterReconciler) getGatewayParentRef(spec tsv1alpha1.HttpRouteSpec, ts *tsv1alpha1.TypesenseCluster) gatewayv1.ParentReference {
	parentRef := gatewayv1.ParentReference{
		Name:        gatewayv1.ObjectName(spec.ParentRef.Name),
		SectionName: spec.ParentRef.SectionName,
	}

	ns := gatewayv1.Namespace(ts.Namespace)
	if spec.ParentRef.Namespace != nil {
		ns = *spec.ParentRef.Namespace
	}
	parentRef.Namespace = &ns

	return parentRef
}

func (r *TypesenseClusterReconciler) createReferenceGrant(ctx context.Context, spec tsv1alpha1.HttpRouteSpec, ts *tsv1alpha1.TypesenseCluster) error {
	parentRefName := gatewayv1beta1.ObjectName(spec.ParentRef.Name)
	referenceGrant := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: getReferenceGrantObjectMeta(ts, spec),
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{
				{
					Group:     gatewayv1beta1.GroupName,
					Kind:      gatewayv1beta1.Kind(kindHTTPRoute),
					Namespace: gatewayv1beta1.Namespace(ts.Namespace),
				},
			},
			To: []gatewayv1beta1.ReferenceGrantTo{
				{
					Group: gatewayv1beta1.GroupName,
					Kind:  gatewayv1beta1.Kind("Gateway"),
					Name:  &parentRefName,
				},
			},
		},
	}

	// ### IMPORTANT ###
	// We cannot reference as owner HTTPRoute or TypesenseCluster because the ReferenceGrant
	// have to be in the same namespace as Gateway, and cross-domain ownerships are
	// not allowed.

	err := r.Create(ctx, referenceGrant)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) deleteReferenceGrant(ctx context.Context, rg *gatewayv1beta1.ReferenceGrant) error {
	err := r.Delete(ctx, rg)
	return client.IgnoreNotFound(err)
}

// isReferenceGrantOwnedBy reports whether rg was created for ts. ReferenceGrants are listed across
// namespaces by their "app" label, which is identical for equally named clusters in different
// namespaces, so the grant's HTTPRoute source namespace is what tells them apart.
func isReferenceGrantOwnedBy(rg *gatewayv1beta1.ReferenceGrant, ts *tsv1alpha1.TypesenseCluster) bool {
	for _, from := range rg.Spec.From {
		if from.Kind == kindHTTPRoute && string(from.Namespace) == ts.Namespace {
			return true
		}
	}

	return false
}

func (r *TypesenseClusterReconciler) deleteOrphanedReferenceGrants(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	if deployed, err := r.IsApiGroupDeployed(gatewayApiGroup); err != nil || !deployed {
		return nil
	}

	referenceGrantsLabelSelector := labels.SelectorFromSet(map[string]string{
		"app": fmt.Sprintf(ClusterAppLabel, ts.Name),
	})

	var referenceGrants gatewayv1beta1.ReferenceGrantList
	if err := r.List(ctx, &referenceGrants, &client.ListOptions{
		LabelSelector: referenceGrantsLabelSelector,
	}); err != nil {
		gerr := fmt.Errorf("failed to list scoped reference grants: %w", err)
		r.logger.Error(gerr, "cleaning up reference grants failed")
		return gerr
	}

	// If the cluster is being deleted, remove all associated ReferenceGrants
	if !ts.DeletionTimestamp.IsZero() {
		for i := range referenceGrants.Items {
			grant := &referenceGrants.Items[i]
			if !isReferenceGrantOwnedBy(grant, ts) {
				continue
			}
			_ = r.deleteReferenceGrant(ctx, grant)
		}
		return nil
	}

	for _, rg := range referenceGrants.Items {
		if !isReferenceGrantOwnedBy(&rg, ts) {
			continue
		}

		exists := false
		for _, droute := range ts.Spec.HttpRoutes {
			if rg.Name == getReferenceGrantName(ts, droute.Name) {
				exists = true
				break
			}
		}

		if !exists {
			grant := rg
			err := r.deleteReferenceGrant(ctx, &grant)
			if err != nil {
				gerr := fmt.Errorf("deleting reference grant failed: %w", err)
				r.logger.Error(gerr, "reconciling http routes failed")
				return gerr
			}
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) updateReferenceGrant(ctx context.Context, spec tsv1alpha1.HttpRouteSpec, ts *tsv1alpha1.TypesenseCluster) error {
	exists := true
	cre := false
	del := false

	name := getReferenceGrantName(ts, spec.Name)
	namespace := ts.Namespace
	if spec.ParentRef.Namespace != nil {
		namespace = string(*spec.ParentRef.Namespace)
	}
	objectKey := client.ObjectKey{Namespace: namespace, Name: name}

	var referenceGrant gatewayv1beta1.ReferenceGrant
	err := r.Get(ctx, objectKey, &referenceGrant)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		exists = false
	}

	refGrant := ptr.Deref(spec.ReferenceGrant, false)

	if exists && refGrant || !exists && !refGrant {
		return nil
	}

	if !exists && refGrant {
		cre = true
	}

	if exists && !refGrant {
		del = true
	}

	if cre {
		err := r.createReferenceGrant(ctx, spec, ts)
		if err != nil {
			r.logger.Error(err, "creating reference grant failed", "http_route", spec.Name)
			return err
		}

		return nil
	}

	if del {
		err := r.deleteReferenceGrant(ctx, &referenceGrant)
		if err != nil {
			r.logger.Error(err, "deleting reference grant failed", "http_route", spec.Name)
			return err
		}
	}

	return nil
}
