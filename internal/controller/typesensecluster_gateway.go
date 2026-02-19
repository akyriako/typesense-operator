package controller

import (
	"context"
	"fmt"
	"maps"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	minimumSupportedVersionForGateway = "1.26.0"
)

func (r *TypesenseClusterReconciler) ReconcileGateway(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (err error) {
	if supported, ver, err := r.IsFeatureSupported(minimumSupportedVersionForGateway); !supported || err != nil {
		if err != nil {
			return err
		}

		r.logger.Info("gateway is not supported in kubernetes current version", "current", ver, "minimum_required", fmt.Sprintf("v%s", minimumSupportedVersionForGateway))
		return nil
	}

	r.logger.V(debugLevel).Info("reconciling gateway")

	httpRouteName := fmt.Sprintf(ClusterHttpRoute, ts.Name)
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

	if httpRouteExists && ts.Spec.Gateway == nil {
		return r.deleteHttpRoute(ctx, httpRoute)
	} else if !httpRouteExists && ts.Spec.Gateway == nil {
		return nil
	}

	if !httpRouteExists {
		r.logger.V(debugLevel).Info("creating http route", "http_route", httpRouteName)

		_, err = r.createHttpRoute(ctx, httpRouteObjectKey, ts)
		if err != nil {
			r.logger.Error(err, "creating http route failed", "http_route", httpRouteName)
			return err
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createHttpRoute(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*gatewayv1.HTTPRoute, error) {
	annotations := map[string]string{}
	if ts.Spec.Gateway.Annotations != nil {
		maps.Copy(annotations, ts.Spec.Gateway.Annotations)
	}

	parentRef := gatewayv1.ParentReference{
		Name:        gatewayv1.ObjectName(ts.Spec.Gateway.ParentRef.Name),
		SectionName: ts.Spec.Gateway.ParentRef.SectionName,
	}

	ns := gatewayv1.Namespace(ts.Namespace)
	if ts.Spec.Gateway.ParentRef.Namespace != nil {
		ns = *ts.Spec.Gateway.ParentRef.Namespace
	}
	parentRef.Namespace = &ns

	hostnames := make([]gatewayv1.Hostname, 0, len(ts.Spec.Gateway.Hostnames))
	for _, h := range ts.Spec.Gateway.Hostnames {
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
								Type:  ts.Spec.Gateway.PathType,
								Value: &ts.Spec.Gateway.Path,
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
