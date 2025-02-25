package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const prometheusApiGroup = "monitoring.coreos.com"

func (r *TypesenseClusterReconciler) ReconcilePodMonitor(ctx context.Context, ts tsv1alpha1.TypesenseCluster) error {
	r.logger.V(debugLevel).Info("reconciling podmonitor")

	// TODO Remove in future version 0.2.15
	r.deleteMetricsExporterServiceMonitor(ctx, ts)

	if ts.Spec.Metrics != nil {
		if deployed, err := r.IsPrometheusDeployed(); err != nil || !deployed {
			err := fmt.Errorf("prometheus api group %s was not found in cluster", prometheusApiGroup)
			r.logger.Error(err, "reconciling podmonitor skipped")
			return nil
		}
	}

	podMonitorName := fmt.Sprintf(ClusterMetricsPodMonitor, ts.Name)
	podMonitorExists := true
	podMonitorObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: podMonitorName}

	var podMonitor = &monitoringv1.PodMonitor{}
	if err := r.Get(ctx, podMonitorObjectKey, podMonitor); err != nil {
		if apierrors.IsNotFound(err) {
			podMonitorExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch podmonitor: %s", podMonitorName))
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
		r.logger.V(debugLevel).Info("creating podmonitor", "podmonitor", podMonitorObjectKey.Name)

		err := r.createMetricsExporterPodMonitor(ctx, podMonitorObjectKey, &ts)
		if err != nil {
			r.logger.Error(err, "creating podmonitor failed", "podmonitor", podMonitorObjectKey.Name)
			return err
		}
	} else {
		if ts.Spec.Metrics.Release != podMonitor.ObjectMeta.Labels["release"] || monitoringv1.Duration(fmt.Sprintf("%ds", ts.Spec.Metrics.IntervalInSeconds)) != podMonitor.Spec.PodMetricsEndpoints[0].Interval {
			r.logger.V(debugLevel).Info("updating podmonitor", "podmonitor", podMonitorObjectKey.Name)

			err := r.deleteMetricsExporterPodMonitor(ctx, podMonitor)
			if err != nil {
				r.logger.Error(err, "deleting podmonitor failed", "podmonitor", podMonitorObjectKey.Name)
				return err
			}

			err = r.createMetricsExporterPodMonitor(ctx, podMonitorObjectKey, &ts)
			if err != nil {
				r.logger.Error(err, "creating podmonitor failed", "podmonitor", podMonitorObjectKey.Name)
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
					Port:     "metrics",
					Path:     "/metrics",
					Interval: monitoringv1.Duration(fmt.Sprintf("%ds", ts.Spec.Metrics.IntervalInSeconds)),
					Scheme:   "http",
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
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) IsPrometheusDeployed() (bool, error) {
	apiGroupList, err := r.DiscoveryClient.ServerGroups()
	if err != nil {
		return false, err
	}

	for _, apiGroup := range apiGroupList.Groups {
		if apiGroup.Name == prometheusApiGroup {
			return true, nil
		}
	}

	return false, nil
}

// TODO Remove in future version 0.2.15
func (r *TypesenseClusterReconciler) deleteMetricsExporterServiceMonitor(ctx context.Context, ts tsv1alpha1.TypesenseCluster) {
	deploymentName := fmt.Sprintf(ClusterPrometheusExporterDeployment, ts.Name)
	deploymentExists := true
	deploymentObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: deploymentName}

	var deployment = &appsv1.Deployment{}
	if err := r.Get(ctx, deploymentObjectKey, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			deploymentExists = false
		} else {
			r.logger.V(debugLevel).Error(err, fmt.Sprintf("unable to fetch metrics exporter deployment: %s", deploymentName))
		}
	}

	if deploymentExists {
		err := r.deleteMetricsExporterDeployment(ctx, deployment)
		if err != nil {
			r.logger.V(debugLevel).Error(err, fmt.Sprintf("unable to cleanup metrics exporter deployment: %s", deploymentName))
		}
	}
}

// TODO Remove in future version 0.2.15
func (r *TypesenseClusterReconciler) deleteMetricsExporterDeployment(ctx context.Context, deployment *appsv1.Deployment) error {
	err := r.Delete(ctx, deployment)
	if err != nil {
		return err
	}

	return nil
}
