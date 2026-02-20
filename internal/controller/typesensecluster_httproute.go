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
)

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
		if ts.Spec.HttpRoutes != nil || len(ts.Spec.HttpRoutes) != 0 {
			err := fmt.Errorf("gateway api group %s was not found in cluster", gatewayApiGroup)
			r.logger.Error(err, "reconciling http routes skipped")
		}
		return nil
	}

	r.logger.V(debugLevel).Info("reconciling http routes")

	labelSelector := labels.SelectorFromSet(getLabels(ts))
	var httpRoutes gatewayv1.HTTPRouteList
	if err = r.List(ctx, &httpRoutes, &client.ListOptions{
		Namespace:     ts.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		gerr := fmt.Errorf("failed to list http routes: %w", err)
		r.logger.Error(gerr, "reconciling http routes skipped")
		return gerr
	}

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
			err = r.deleteHttpRoute(ctx, &eroute)
			if err != nil {
				gerr := fmt.Errorf("deleting http route failed: %w", err)
				r.logger.Error(gerr, "reconciling http routes skipped")
				return gerr
			}
		}
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

			httpRoute, err = r.createHttpRoute(ctx, httpRouteObjectKey, hrt, ts)
			if err != nil {
				r.logger.Error(err, "creating http route failed", "http_route", httpRouteName)
				return err
			}
			_, err := r.createReferenceGrant(ctx, hrt, httpRoute, ts)
			if err != nil {
				r.logger.Error(err, "creating reference grant failed", "http_route", httpRouteName)
				return err
			}

		} else {
			if !hrt.Enabled {
				err = r.deleteHttpRoute(ctx, httpRoute)
				if err != nil {
					gerr := fmt.Errorf("deleting http route failed: %w", err)
					r.logger.Error(gerr, "reconciling http routes failed")
					return gerr
				}
			}

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
			path := *httpRoute.Spec.Rules[0].Matches[0].Path.Value
			pathType := httpRoute.Spec.Rules[0].Matches[0].Path.Type

			if !apiequality.Semantic.DeepEqual(hostnames, httpRoute.Spec.Hostnames) ||
				!apiequality.Semantic.DeepEqual(hrt.Annotations, annotations) ||
				!apiequality.Semantic.DeepEqual(parentRef, httpRoute.Spec.ParentRefs[0]) ||
				hrt.Path != path || *hrt.PathType != *pathType {

				r.logger.V(debugLevel).Info("updating http route", "http_route", httpRouteName)

				httpRoute, err = r.updateHttpRoute(ctx, hrt, httpRoute, ts)
				if err != nil {
					r.logger.Error(err, "updating http route failed", "http_route", httpRouteName)
					return err
				}
			}
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createHttpRoute(ctx context.Context, key client.ObjectKey, spec tsv1alpha1.HttpRouteSpec, ts *tsv1alpha1.TypesenseCluster) (*gatewayv1.HTTPRoute, error) {
	annotations := map[string]string{}
	if spec.Annotations != nil {
		maps.Copy(annotations, spec.Annotations)
	}

	parentRef := r.getGatewayParentRef(spec, ts)

	hostnames := make([]gatewayv1.Hostname, 0, len(spec.Hostnames))
	for _, h := range spec.Hostnames {
		hostnames = append(hostnames, gatewayv1.Hostname(h))
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
		ObjectMeta: getObjectMeta(ts, &key.Name, annotations),
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
								Type:  spec.PathType,
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

	err = r.Create(ctx, httpRoute)
	if err != nil {
		return nil, err
	}

	return httpRoute, nil
}

func (r *TypesenseClusterReconciler) deleteHttpRoute(ctx context.Context, httpRoute *gatewayv1.HTTPRoute) error {
	err := r.Delete(ctx, httpRoute)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) updateHttpRoute(ctx context.Context, spec tsv1alpha1.HttpRouteSpec, httpRoute *gatewayv1.HTTPRoute, ts *tsv1alpha1.TypesenseCluster) (*gatewayv1.HTTPRoute, error) {
	patch := client.MergeFrom(httpRoute.DeepCopy())

	parentRef := r.getGatewayParentRef(spec, ts)
	httpRoute.Spec.CommonRouteSpec.ParentRefs[0] = parentRef

	hostnames := make([]gatewayv1.Hostname, 0, len(spec.Hostnames))
	for _, h := range spec.Hostnames {
		hostnames = append(hostnames, gatewayv1.Hostname(h))
	}
	httpRoute.Spec.Hostnames = hostnames

	httpRoute.Spec.Rules[0].Matches[0].Path.Value = &spec.Path
	httpRoute.Spec.Rules[0].Matches[0].Path.Type = spec.PathType

	annotations := map[string]string{}
	if spec.Annotations != nil {
		maps.Copy(annotations, spec.Annotations)
	}
	httpRoute.Annotations = annotations

	if err := r.Patch(ctx, httpRoute, patch); err != nil {
		return nil, err
	}

	return httpRoute, nil
}

func (r *TypesenseClusterReconciler) getHttpRouteAnnotations(httpRoute *gatewayv1.HTTPRoute, ts *tsv1alpha1.TypesenseCluster) map[string]string {
	filters := append([]string{clusterIssuerAnnotationKey, rancherDomainAnnotationKey}, ts.Spec.IgnoreAnnotationsFromExternalMutations...)
	filtered := filterAnnotations(httpRoute.Annotations, filters...)
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

func (r *TypesenseClusterReconciler) createReferenceGrant(ctx context.Context, spec tsv1alpha1.HttpRouteSpec, httpRoute *gatewayv1.HTTPRoute, ts *tsv1alpha1.TypesenseCluster) (*gatewayv1beta1.ReferenceGrant, error) {
	parentRefName := gatewayv1beta1.ObjectName(spec.ParentRef.Name)
	referenceGrant := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(ClusterHttpRouteReferenceGrant, ts.Name, spec.Name),
			Namespace: string(*spec.ParentRef.Namespace), // namespace of the *target* (Gateway)
		},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{
				{
					Group:     gatewayv1beta1.GroupName,
					Kind:      gatewayv1beta1.Kind("HTTPRoute"),
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

	//err := ctrl.SetControllerReference(httpRoute, referenceGrant, r.Scheme)
	//if err != nil {
	//	return nil, err
	//}

	err := r.Create(ctx, referenceGrant)
	if err != nil {
		return nil, err
	}

	return referenceGrant, nil
}
