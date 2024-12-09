package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"maps"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	conf = `events {}
				http {
				  server {
					listen 80;

					%s
					
					location / {
					  proxy_pass http://%s-svc:8108/;
					  proxy_pass_request_headers on;
					}
				  }
				}`

	referer = `valid_referers server_names %s;   
					if ($invalid_referer) {  
				  		return 403;     
					}`
)

func (r *TypesenseClusterReconciler) ReconcileIngress(ctx context.Context, ts tsv1alpha1.TypesenseCluster) (err error) {
	r.logger.V(debugLevel).Info("reconciling ingress")

	ingressName := fmt.Sprintf("%s-reverse-proxy", ts.Name)
	ingressExists := true
	ingressObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: ingressName}

	var ig = &networkingv1.Ingress{}
	if err := r.Get(ctx, ingressObjectKey, ig); err != nil {
		if apierrors.IsNotFound(err) {
			ingressExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress: %s", ingressName))
		}
	}

	if ingressExists && ts.Spec.Ingress == nil {
		return r.deleteIngress(ctx, ig)
	} else if !ingressExists && ts.Spec.Ingress == nil {
		return nil
	}

	if !ingressExists {
		r.logger.V(debugLevel).Info("creating ingress", "ingress", ingressObjectKey.Name)

		ig, err = r.createIngress(ctx, ingressObjectKey, &ts)
		if err != nil {
			r.logger.Error(err, "creating ingress failed", "ingress", ingressObjectKey.Name)
			return err
		}
	}

	configMapName := fmt.Sprintf("%s-reverse-proxy-config", ts.Name)
	configMapExists := true
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		if apierrors.IsNotFound(err) {
			configMapExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress config map: %s", configMapName))
		}
	}

	if !configMapExists {
		r.logger.V(debugLevel).Info("creating ingress config map", "configmap", configMapObjectKey.Name)

		_, err = r.createIngressConfigMap(ctx, configMapObjectKey, &ts, ig)
		if err != nil {
			r.logger.Error(err, "creating ingress config map failed", "configmap", configMapObjectKey.Name)
			return err
		}
	} else {
		r.logger.V(debugLevel).Info("updating ingress config map", "configmap", configMapObjectKey.Name)

		_, err = r.updateIngressConfigMap(ctx, cm, &ts)
		if err != nil {
			return err
		}
	}

	deploymentName := fmt.Sprintf("%s-reverse-proxy", ts.Name)
	deploymentExists := true
	deploymentObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: deploymentName}

	var deployment = &appsv1.Deployment{}
	if err := r.Get(ctx, deploymentObjectKey, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			deploymentExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress reverse proxy deployment: %s", deploymentName))
		}
	}

	if !deploymentExists {
		r.logger.V(debugLevel).Info("creating ingress reverse proxy deployment", "deployment", deploymentObjectKey.Name)

		_, err = r.createIngressDeployment(ctx, deploymentObjectKey, &ts, ig)
		if err != nil {
			r.logger.Error(err, "creating ingress reverse proxy deployment failed", "deployment", deploymentObjectKey.Name)
			return err
		}
	}

	serviceName := fmt.Sprintf("%s-reverse-proxy-svc", ts.Name)
	serviceExists := true
	serviceNameObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: serviceName}

	var service = &v1.Service{}
	if err := r.Get(ctx, serviceNameObjectKey, service); err != nil {
		if apierrors.IsNotFound(err) {
			serviceExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress reverse proxy service: %s", serviceName))
		}
	}

	if !serviceExists {
		r.logger.V(debugLevel).Info("creating ingress reverse proxy service", "service", serviceNameObjectKey.Name)

		_, err = r.createIngressService(ctx, serviceNameObjectKey, &ts, ig)
		if err != nil {
			r.logger.Error(err, "creating ingress reverse proxy service failed", "service", serviceNameObjectKey.Name)
			return err
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createIngress(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*networkingv1.Ingress, error) {
	annotations := map[string]string{}
	annotations["cert-manager.io/cluster-issuer"] = ts.Spec.Ingress.ClusterIssuer

	if ts.Spec.Ingress.Annotations != nil {
		maps.Copy(annotations, ts.Spec.Ingress.Annotations)
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: getObjectMeta(ts, &key.Name, annotations),
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(ts.Spec.Ingress.IngressClassName),
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{ts.Spec.Ingress.Host},
					SecretName: fmt.Sprintf("%s-reverse-proxy-%s-certificate-tls", ts.Name, ts.Spec.Ingress.ClusterIssuer),
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: ts.Spec.Ingress.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: ptr.To[networkingv1.PathType](networkingv1.PathTypeImplementationSpecific),
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: fmt.Sprintf("%s-reverse-proxy-svc", ts.Name),
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, ingress, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, ingress)
	if err != nil {
		return nil, err
	}

	return ingress, nil
}

func (r *TypesenseClusterReconciler) deleteIngress(ctx context.Context, ig *networkingv1.Ingress) error {
	err := r.Delete(ctx, ig)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) createIngressConfigMap(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, ig *networkingv1.Ingress) (*v1.ConfigMap, error) {
	icm := &v1.ConfigMap{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Data: map[string]string{
			"nginx.conf": r.getIngressNginxConf(ts),
		},
	}

	err := ctrl.SetControllerReference(ig, icm, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, icm)
	if err != nil {
		return nil, err
	}

	return icm, nil
}

func (r *TypesenseClusterReconciler) updateIngressConfigMap(ctx context.Context, cm *v1.ConfigMap, ts *tsv1alpha1.TypesenseCluster) (*v1.ConfigMap, error) {
	desired := cm.DeepCopy()
	desired.Data = map[string]string{
		"nginx.conf": r.getIngressNginxConf(ts),
	}

	if cm.Data["nginx.conf"] != desired.Data["nginx.conf"] {
		err := r.Update(ctx, desired)
		if err != nil {
			r.logger.Error(err, "updating ingress config map failed")
			return nil, err
		}
	}

	return desired, nil
}

func (r *TypesenseClusterReconciler) getIngressNginxConf(ts *tsv1alpha1.TypesenseCluster) string {
	ref := ""
	if ts.Spec.Ingress != nil && ts.Spec.Ingress.Referer != nil {
		ref = fmt.Sprintf(referer, *ts.Spec.Ingress.Referer)
	}

	return fmt.Sprintf(conf, ref, ts.Name)
}

func (r *TypesenseClusterReconciler) createIngressDeployment(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, ig *networkingv1.Ingress) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](3),
			Selector: &metav1.LabelSelector{
				MatchLabels: getReverseProxyLabels(ts),
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: getReverseProxyLabels(ts),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  fmt.Sprintf("%s-reverse-proxy", ts.Name),
							Image: "nginx:alpine",
							Ports: []v1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "nginx-config",
									MountPath: "/etc/nginx/nginx.conf",
									SubPath:   "nginx.conf",
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: "nginx-config",
							VolumeSource: v1.VolumeSource{
								ConfigMap: &v1.ConfigMapVolumeSource{
									LocalObjectReference: v1.LocalObjectReference{
										Name: fmt.Sprintf("%s-reverse-proxy-config", ts.Name),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ig, deployment, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, deployment)
	if err != nil {
		return nil, err
	}

	return deployment, nil
}

func (r *TypesenseClusterReconciler) createIngressService(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, ig *networkingv1.Ingress) (*v1.Service, error) {
	service := &v1.Service{
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: v1.ServiceSpec{
			Type:     v1.ServiceTypeNodePort,
			Selector: getReverseProxyLabels(ts),
			Ports: []v1.ServicePort{
				{
					Protocol:   v1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: int32(80)},
					Name:       "http",
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ig, service, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, service)
	if err != nil {
		return nil, err
	}

	return service, nil
}
