package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
)

func (r *TypesenseClusterReconciler) ReconcileMetricsExporter(ctx context.Context, ts tsv1alpha1.TypesenseCluster) error {
	r.logger.V(debugLevel).Info("reconciling metrics exporter")

	//if !r.IsPrometheusDeployed(ctx) {
	//	err := fmt.Errorf("monitoring.coreos.com custom resources were not found in cluster")
	//	r.logger.Error(err, "reconciling metrics exporter skipped")
	//	return nil
	//}

	deploymentName := fmt.Sprintf(ClusterPrometheusExporterDeployment, ts.Name)
	deploymentExists := true
	deploymentObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: deploymentName}

	var deployment = &appsv1.Deployment{}
	if err := r.Get(ctx, deploymentObjectKey, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			deploymentExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch metrics exporter deployment: %s", deploymentName))
			return err
		}
	}

	if !ts.Spec.EnableMetricsExporter {
		if deploymentExists {
			err := r.deleteMetricsExporterDeployment(ctx, deployment)
			if err != nil {
				return err
			}
		}

		return nil
	}

	if !deploymentExists {
		r.logger.V(debugLevel).Info("creating metrics exporter deployment", "deployment", deploymentObjectKey.Name)

		dpl, err := r.createMetricsExporterDeployment(ctx, deploymentObjectKey, &ts)
		if err != nil {
			r.logger.Error(err, "creating metrics exporter deployment failed", "deployment", deploymentObjectKey.Name)
			return err
		}

		deployment = dpl
	}

	serviceName := fmt.Sprintf(ClusterPrometheusExporterService, ts.Name)
	serviceExists := true
	serviceNameObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: serviceName}

	var service = &v1.Service{}
	if err := r.Get(ctx, serviceNameObjectKey, service); err != nil {
		if apierrors.IsNotFound(err) {
			serviceExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch metrics exporter service: %s", serviceName))
			return err
		}
	}

	if !serviceExists {
		r.logger.V(debugLevel).Info("creating metrics exporter service", "service", serviceNameObjectKey.Name)

		err := r.createMetricsExporterService(ctx, serviceNameObjectKey, &ts, deployment)
		if err != nil {
			r.logger.Error(err, "creating  metrics exporter service failed", "service", serviceNameObjectKey.Name)
			return err
		}
	}

	serviceMonitorName := fmt.Sprintf(ClusterPrometheusExporterServiceMonitor, ts.Name)
	serviceMonitorExists := true
	serviceMonitorObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: serviceMonitorName}

	var serviceMonitor = &monitoringv1.ServiceMonitor{}
	if err := r.Get(ctx, serviceMonitorObjectKey, serviceMonitor); err != nil {
		if apierrors.IsNotFound(err) {
			serviceMonitorExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch metrics exporter service: %s", serviceMonitorName))
			return err
		}
	}

	if !serviceMonitorExists {
		r.logger.V(debugLevel).Info("creating metrics exporter servicemonitor", "servicemonitor", serviceMonitorObjectKey.Name)

		err := r.createMetricsExporterServiceMonitor(ctx, serviceMonitorObjectKey, &ts, deployment)
		if err != nil {
			r.logger.Error(err, "creating metrics exporter servicemonitor failed", "servicemonitor", serviceMonitorObjectKey.Name)
			return err
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createMetricsExporterDeployment(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{
				MatchLabels: getMetricsExporterLabels(ts),
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: getMetricsExporterLabels(ts),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "typesense-prometheus-exporter",
							Image: "akyriako78/typesense-prometheus-exporter:0.1.6",
							Env: []v1.EnvVar{
								{
									Name: "TYPESENSE_API_KEY",
									ValueFrom: &v1.EnvVarSource{
										SecretKeyRef: &v1.SecretKeySelector{
											Key: ClusterAdminApiKeySecretKeyName,
											LocalObjectReference: v1.LocalObjectReference{
												Name: r.getAdminApiKeyObjectKey(ts).Name,
											},
										},
									},
								},
								{
									Name:  "TYPESENSE_HOST",
									Value: fmt.Sprintf(ClusterRestService, ts.Name),
								},
								{
									Name:  "TYPESENSE_PORT",
									Value: strconv.Itoa(ts.Spec.ApiPort),
								},
								{
									Name:  "TYPESENSE_PROTOCOL",
									Value: "http",
								},
								{
									Name:  "TYPESENSE_CLUSTER",
									Value: ts.Name,
								},
							},
							Ports: []v1.ContainerPort{
								{
									ContainerPort: 8908,
								},
							},
						},
					},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, deployment, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, deployment)
	if err != nil {
		return nil, err
	}

	return deployment, nil
}

func (r *TypesenseClusterReconciler) deleteMetricsExporterDeployment(ctx context.Context, deployment *appsv1.Deployment) error {
	err := r.Delete(ctx, deployment)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) createMetricsExporterService(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, deployment *appsv1.Deployment) error {
	service := &v1.Service{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: v1.ServiceSpec{
			Type:     v1.ServiceTypeClusterIP,
			Selector: getMetricsExporterLabels(ts),
			Ports: []v1.ServicePort{
				{
					Protocol:   v1.ProtocolTCP,
					Port:       8908,
					TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: int32(8908)},
					Name:       "metrics",
				},
			},
		},
	}

	err := ctrl.SetControllerReference(deployment, service, r.Scheme)
	if err != nil {
		return err
	}

	err = r.Create(ctx, service)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) createMetricsExporterServiceMonitor(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, deployment *appsv1.Deployment) error {
	serviceMonitor := &monitoringv1.ServiceMonitor{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: getMetricsExporterLabels(ts),
			},
			NamespaceSelector: monitoringv1.NamespaceSelector{
				MatchNames: []string{ts.Namespace},
			},
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:     "metrics",
					Path:     "/metrics",
					Interval: "15s",
					Scheme:   "http",
				},
			},
			JobLabel: "app",
		},
	}

	err := ctrl.SetControllerReference(deployment, serviceMonitor, r.Scheme)
	if err != nil {
		return err
	}

	err = r.Create(ctx, serviceMonitor)
	if err != nil {
		return err
	}

	return nil
}

//func (r *TypesenseClusterReconciler) IsCrdDeployed(ctx context.Context, name string) bool {
//	crd := &apiextensions.CustomResourceDefinition{}
//	err := r.Get(ctx, client.ObjectKey{Name: name}, crd)
//	return err == nil
//}
//
//func (r *TypesenseClusterReconciler) IsPrometheusDeployed(ctx context.Context) bool {
//	return r.IsCrdDeployed(ctx, "servicemonitors.monitoring.coreos.com")
//}
