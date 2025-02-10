package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TypesenseClusterReconciler) ReconcileServices(ctx context.Context, ts tsv1alpha1.TypesenseCluster) error {
	r.logger.V(debugLevel).Info("reconciling services")

	headlessSvcName := fmt.Sprintf(ClusterHeadlessService, ts.Name)
	headlessExists := true
	headlessObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: headlessSvcName}

	var headless = &v1.Service{}
	if err := r.Get(ctx, headlessObjectKey, headless); err != nil {
		if apierrors.IsNotFound(err) {
			headlessExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch service: %s", headlessSvcName))
			return err
		}
	}

	if !headlessExists {
		r.logger.V(debugLevel).Info("creating headless service", "service", headlessObjectKey.Name)

		_, err := r.createHeadlessService(ctx, headlessObjectKey, &ts)
		if err != nil {
			r.logger.Error(err, "creating headless service failed", "service", headlessObjectKey.Name)
			return err
		}
	} else {
		if int32(ts.Spec.ApiPort) != headless.Spec.Ports[0].Port {
			r.logger.V(debugLevel).Info("updating headless service", "service", headlessObjectKey.Name)

			err := r.updateHeadlessService(ctx, *headless, &ts)
			if err != nil {
				r.logger.Error(err, "updating headless service failed", "service", headlessObjectKey.Name)
				return err
			}
		}
	}

	svcName := fmt.Sprintf(ClusterRestService, ts.Name)
	svcExists := true
	svcObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: svcName}

	var svc = &v1.Service{}
	if err := r.Get(ctx, svcObjectKey, svc); err != nil {
		if apierrors.IsNotFound(err) {
			svcExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch service: %s", svcName))
			return err
		}
	}

	if !svcExists {
		r.logger.V(debugLevel).Info("creating resolver service", "service", svcObjectKey.Name)

		_, err := r.createService(ctx, svcObjectKey, &ts)
		if err != nil {
			r.logger.Error(err, "creating resolver service failed", "service", svcObjectKey.Name)
			return err
		}
	} else {
		if int32(ts.Spec.ApiPort) != svc.Spec.Ports[0].Port {
			r.logger.V(debugLevel).Info("updating resolver service", "service", svcObjectKey.Name)

			err := r.updateService(ctx, *svc, &ts)
			if err != nil {
				r.logger.Error(err, "updating resolver service failed", "service", svcObjectKey.Name)
				return err
			}
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createHeadlessService(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*v1.Service, error) {
	svc := &v1.Service{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: v1.ServiceSpec{
			ClusterIP:                v1.ClusterIPNone,
			PublishNotReadyAddresses: true,
			Selector:                 getLabels(ts),
			Ports: []v1.ServicePort{
				{
					Name:       "http",
					Port:       int32(ts.Spec.ApiPort),
					TargetPort: intstr.IntOrString{IntVal: 8108},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, svc, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, svc)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

func (r *TypesenseClusterReconciler) updateHeadlessService(ctx context.Context, headless v1.Service, ts *tsv1alpha1.TypesenseCluster) error {
	patch := client.MergeFrom(headless.DeepCopy())
	headless.Spec.Ports[0].Port = int32(ts.Spec.ApiPort)

	if err := r.Patch(ctx, &headless, patch); err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) createService(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*v1.Service, error) {
	svc := &v1.Service{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: v1.ServiceSpec{
			Type:     v1.ServiceTypeClusterIP,
			Selector: getLabels(ts),
			Ports: []v1.ServicePort{
				{
					Name:       "http",
					Port:       int32(ts.Spec.ApiPort),
					TargetPort: intstr.IntOrString{IntVal: 8108},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, svc, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, svc)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

func (r *TypesenseClusterReconciler) updateService(ctx context.Context, svc v1.Service, ts *tsv1alpha1.TypesenseCluster) error {
	patch := client.MergeFrom(svc.DeepCopy())
	svc.Spec.Ports[0].Port = int32(ts.Spec.ApiPort)

	if err := r.Patch(ctx, &svc, patch); err != nil {
		return err
	}

	return nil
}
