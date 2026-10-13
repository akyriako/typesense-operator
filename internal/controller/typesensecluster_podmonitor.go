package controller

import (
	"context"
	"fmt"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const prometheusApiGroup = "monitoring.coreos.com"

func (r *TypesenseClusterReconciler) ReconcilePodMonitor(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	if deployed, err := r.IsApiGroupDeployed(prometheusApiGroup); err != nil || !deployed {
		if ts.Spec.Metrics != nil {
			err := fmt.Errorf("prometheus api group %s was not found in cluster", prometheusApiGroup)
			log.FromContext(ctx).Error(err, "reconciling podmonitor skipped")
		}
		return nil
	}

	log.FromContext(ctx).V(debugLevel).Info("reconciling podmonitor")

	podMonitorName := fmt.Sprintf(ClusterMetricsPodMonitor, ts.Name)
	podMonitorExists := true
	podMonitorObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: podMonitorName}

	var podMonitor = &monitoringv1.PodMonitor{}
	if err := r.Get(ctx, podMonitorObjectKey, podMonitor); err != nil {
		if apierrors.IsNotFound(err) {
			podMonitorExists = false
		} else {
			log.FromContext(ctx).Error(err, fmt.Sprintf("unable to fetch podmonitor: %s", podMonitorName))
			return err
		}
	}

	if ts.Spec.Metrics == nil {
		if podMonitorExists {
			err := r.deleteMetricsExporterPodMonitor(ctx, podMonitor)
			if err != nil {
				return err
			}
		}

		return nil
	}

	if !podMonitorExists {
		log.FromContext(ctx).V(debugLevel).Info("creating podmonitor", "podmonitor", podMonitorObjectKey.Name)

		err := r.createMetricsExporterPodMonitor(ctx, podMonitorObjectKey, ts)
		if err != nil {
			log.FromContext(ctx).Error(err, "creating podmonitor failed", "podmonitor", podMonitorObjectKey.Name)
			return err
		}
	} else {
		if ts.Spec.Metrics.Release != podMonitor.Labels["release"] ||
			len(podMonitor.Spec.PodMetricsEndpoints) == 0 ||
			monitoringv1.Duration(fmt.Sprintf("%ds", ts.Spec.Metrics.IntervalInSeconds)) != podMonitor.Spec.PodMetricsEndpoints[0].Interval {
			log.FromContext(ctx).V(debugLevel).Info("updating podmonitor", "podmonitor", podMonitorObjectKey.Name)

			objectMeta := getPodMonitorObjectMeta(ts, &podMonitorObjectKey.Name, nil)
			objectMeta.Labels["release"] = ts.Spec.Metrics.Release

			desired := &monitoringv1.PodMonitor{
				TypeMeta: metav1.TypeMeta{
					APIVersion: monitoringv1.SchemeGroupVersion.String(),
					Kind:       "PodMonitor",
				},
				ObjectMeta: objectMeta,
				Spec: monitoringv1.PodMonitorSpec{
					Selector: metav1.LabelSelector{
						MatchLabels: getLabels(ts),
					},
					NamespaceSelector: monitoringv1.NamespaceSelector{
						MatchNames: []string{ts.Namespace},
					},
					PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
						{
							Port:     ptr.To("metrics"),
							Path:     "/metrics",
							Interval: monitoringv1.Duration(fmt.Sprintf("%ds", ts.Spec.Metrics.IntervalInSeconds)),
							Scheme:   ptr.To(monitoringv1.Scheme(protocolHttp)),
						},
					},
				},
			}

			//nolint:staticcheck
			err := r.Patch(ctx, desired, client.Apply, client.ForceOwnership, client.FieldOwner("typesense-operator"))
			if err != nil {
				log.FromContext(ctx).Error(err, "updating podmonitor failed", "podmonitor", podMonitorObjectKey.Name)
				return err
			}
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createMetricsExporterPodMonitor(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) error {
	objectMeta := getPodMonitorObjectMeta(ts, &key.Name, nil)
	objectMeta.Labels["release"] = ts.Spec.Metrics.Release

	podMonitor := &monitoringv1.PodMonitor{
		ObjectMeta: objectMeta,
		Spec: monitoringv1.PodMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: getLabels(ts),
			},
			NamespaceSelector: monitoringv1.NamespaceSelector{
				MatchNames: []string{ts.Namespace},
			},
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
				{
					Port:     ptr.To("metrics"),
					Path:     "/metrics",
					Interval: monitoringv1.Duration(fmt.Sprintf("%ds", ts.Spec.Metrics.IntervalInSeconds)),
					Scheme:   ptr.To(monitoringv1.Scheme(protocolHttp)),
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, podMonitor, r.Scheme)
	if err != nil {
		return err
	}

	err = r.Create(ctx, podMonitor)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) deleteMetricsExporterPodMonitor(ctx context.Context, podMonitor *monitoringv1.PodMonitor) error {
	err := r.Delete(ctx, podMonitor)
	return client.IgnoreNotFound(err)
}
