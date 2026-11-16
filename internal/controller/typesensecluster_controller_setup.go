package controller

import (
	"context"
	"fmt"
	"reflect"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SetupWithManager sets up the controller with the Manager.
func (r *TypesenseClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	eventFilters := builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return !e.DeleteStateUnknown
		},
	})

	podFilters := predicate.And(
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			labels := obj.GetLabels()

			return labels["app.kubernetes.io/managed-by"] == "typesense-operator" &&
				labels["app.kubernetes.io/name"] == "typesense" &&
				labels["app.kubernetes.io/instance"] != ""
		}),
		predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				if e.IsInInitialList {
					return false
				}

				pod := e.Object.(*corev1.Pod)

				ctrl.Log.V(debugLevel).Info(
					"[watch] pod create event observed",
					"pod", pod.Name,
					"ip", pod.Status.PodIP,
				)
				return true
			},

			UpdateFunc: func(e event.UpdateEvent) bool {
				oldPod := e.ObjectOld.(*corev1.Pod)
				newPod := e.ObjectNew.(*corev1.Pod)

				ipChanged := oldPod.Status.PodIP != newPod.Status.PodIP

				deletionStarted :=
					oldPod.DeletionTimestamp == nil &&
						newPod.DeletionTimestamp != nil

				if ipChanged || deletionStarted {
					ctrl.Log.V(debugLevel).Info(
						"[watch] pod update event observed",
						"pod", newPod.Name,
						"oldIP", oldPod.Status.PodIP,
						"newIP", newPod.Status.PodIP,
						"deletionStarted", deletionStarted,
					)
					return true
				}

				return false
			},

			DeleteFunc: func(e event.DeleteEvent) bool {
				if e.DeleteStateUnknown {
					return false
				}

				ctrl.Log.V(debugLevel).Info(
					"[watch] pod delete event observed",
					"pod", e.Object.GetName(),
				)
				return true
			},

			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		},
	)

	endpointSliceFilters := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			es, ok := e.Object.(*discoveryv1.EndpointSlice)
			return ok && es.Labels[discoveryv1.LabelServiceName] != ""
		},

		UpdateFunc: func(e event.UpdateEvent) bool {
			oldES, okOld := e.ObjectOld.(*discoveryv1.EndpointSlice)
			newES, okNew := e.ObjectNew.(*discoveryv1.EndpointSlice)

			if !okOld || !okNew {
				return false
			}

			changed := oldES.Labels[discoveryv1.LabelServiceName] != newES.Labels[discoveryv1.LabelServiceName] ||
				!reflect.DeepEqual(endpointSliceMembers(oldES), endpointSliceMembers(newES))

			if changed {
				ctrl.Log.V(debugLevel).Info(
					"[watch] endpointslice membership changed",
					"endpointslice", newES.Name,
					"service", newES.Labels[discoveryv1.LabelServiceName],
				)
			}

			return changed
		},

		DeleteFunc: func(e event.DeleteEvent) bool {
			es, ok := e.Object.(*discoveryv1.EndpointSlice)
			return ok && es.Labels[discoveryv1.LabelServiceName] != ""
		},

		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	endpointSliceMapper := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []reconcile.Request {
			es, ok := obj.(*discoveryv1.EndpointSlice)
			if !ok {
				return nil
			}

			serviceName := es.Labels[discoveryv1.LabelServiceName]
			if serviceName == "" {
				return nil
			}

			svc := &corev1.Service{}
			err := r.Get(ctx, client.ObjectKey{
				Namespace: es.Namespace,
				Name:      serviceName,
			}, svc)
			if err != nil {
				return nil
			}

			labels := svc.GetLabels()

			if labels["app.kubernetes.io/managed-by"] != "typesense-operator" ||
				labels["app.kubernetes.io/name"] != "typesense" {
				return nil
			}

			clusterName := labels["app.kubernetes.io/instance"]
			if clusterName == "" {
				return nil
			}

			// Only react to the cluster's headless Service.
			if serviceName != fmt.Sprintf(ClusterHeadlessService, clusterName) {
				return nil
			}

			ctrl.Log.V(debugLevel).Info(
				"[watch] endpointslice event observed",
				"endpointslice", es.Name,
				"service", serviceName,
				"cluster", clusterName,
			)

			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Namespace: es.Namespace,
					Name:      clusterName,
				},
			}}
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&tsv1alpha1.TypesenseCluster{}, eventFilters).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(
				func(ctx context.Context, obj client.Object) []reconcile.Request {
					return []reconcile.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: obj.GetNamespace(),
							Name:      obj.GetLabels()["app.kubernetes.io/instance"],
						},
					}}
				},
			),
			builder.WithPredicates(podFilters),
		).
		Watches(
			&discoveryv1.EndpointSlice{},
			endpointSliceMapper,
			builder.WithPredicates(endpointSliceFilters),
		).
		Named("typesense-kubernetes-operator").
		Complete(r)
}
