package controller

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"text/template"
	"time"

	"reflect"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	confTemplate = `events {}
		http {
		  {{- if .HttpDirectives}}
		  {{.HttpDirectives}}
		  {{- end}}
		  server {
			listen 80;

			{{- if .Referer}}
			{{.Referer}}
			{{- end}}
			{{- if .ServerDirectives}}
			{{.ServerDirectives}}
			{{- end}}
			location / {
			  proxy_pass http://{{.ServiceName}}-svc:{{.ServicePort}}/;
			  proxy_pass_request_headers on;

			  {{- if .LocationDirectives}}
			  {{.LocationDirectives}}
			  {{- end}}
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

	ingressName := fmt.Sprintf(ClusterReverseProxyIngress, ts.Name)
	ingressExists := true
	ingressObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: ingressName}

	var ig = &networkingv1.Ingress{}
	if err := r.Get(ctx, ingressObjectKey, ig); err != nil {
		if apierrors.IsNotFound(err) {
			ingressExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress: %s", ingressName))
			return err
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
	} else {
		if ts.Spec.Ingress.Host != ig.Spec.Rules[0].Host ||
			(ts.Spec.Ingress.ClusterIssuer != nil && *ts.Spec.Ingress.ClusterIssuer != ig.Annotations["cert-manager.io/cluster-issuer"]) ||
			!reflect.DeepEqual(ts.Spec.Ingress.Annotations, r.getIngressAnnotations(ig)) ||
			(ts.Spec.Ingress.TLSSecretName != nil && *ts.Spec.Ingress.TLSSecretName != ig.Spec.TLS[0].SecretName) ||
			ts.Spec.Ingress.IngressClassName != *ig.Spec.IngressClassName {

			r.logger.V(debugLevel).Info("updating ingress", "ingress", ingressObjectKey.Name)

			ig, err = r.updateIngress(ctx, *ig, &ts)
			if err != nil {
				r.logger.Error(err, "updating ingress failed", "ingress", ingressObjectKey.Name)
				return err
			}
		}

	}

	configMapName := fmt.Sprintf(ClusterReverseProxyConfigMap, ts.Name)
	configMapExists := true
	configMapObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: configMapName}

	var cm = &v1.ConfigMap{}
	if err := r.Get(ctx, configMapObjectKey, cm); err != nil {
		if apierrors.IsNotFound(err) {
			configMapExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress config map: %s", configMapName))
			return err
		}
	}

	configMapUpdated := false
	if !configMapExists {
		r.logger.V(debugLevel).Info("creating ingress config map", "configmap", configMapObjectKey.Name)

		_, err = r.createIngressConfigMap(ctx, configMapObjectKey, &ts, ig)
		if err != nil {
			r.logger.Error(err, "creating ingress config map failed", "configmap", configMapObjectKey.Name)
			return err
		}
	} else {
		shouldUpdate, err := r.shouldUpdateIngressConfigMap(cm, &ts)
		if err != nil {
			return err
		}

		if shouldUpdate {
			r.logger.V(debugLevel).Info("updating ingress config map", "configmap", configMapObjectKey.Name)

			_, err = r.updateIngressConfigMap(ctx, cm, &ts)
			if err != nil {
				return err
			}

			configMapUpdated = true
		}
	}

	deploymentName := fmt.Sprintf(ClusterReverseProxy, ts.Name)
	deploymentExists := true
	deploymentObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: deploymentName}

	var deployment = &appsv1.Deployment{}
	if err := r.Get(ctx, deploymentObjectKey, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			deploymentExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress reverse proxy deployment: %s", deploymentName))
			return err
		}
	}

	if !deploymentExists {
		r.logger.V(debugLevel).Info("creating ingress reverse proxy deployment", "deployment", deploymentObjectKey.Name)

		_, err = r.createIngressDeployment(ctx, deploymentObjectKey, &ts, ig)
		if err != nil {
			r.logger.Error(err, "creating ingress reverse proxy deployment failed", "deployment", deploymentObjectKey.Name)
			return err
		}
	} else {
		desiredResources := ts.Spec.Ingress.GetReverseProxyResources()
		deploymentResourcesNeedUpdate := !reflect.DeepEqual(desiredResources, deployment.Spec.Template.Spec.Containers[0].Resources)
		if deploymentResourcesNeedUpdate {
			deployment.Spec.Template.Spec.Containers[0].Resources = desiredResources
		}

		if configMapUpdated || deploymentResourcesNeedUpdate {
			if deployment.Spec.Template.Annotations == nil {
				deployment.Spec.Template.Annotations = make(map[string]string)
			}

			r.logger.V(debugLevel).Info("adding restart annotation to ingress reverse proxy deployment", "deployment", deploymentObjectKey.Name)
			deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

			if err := r.Update(ctx, deployment); err != nil {
				r.logger.Error(err, "adding restart annotation to ingress reverse proxy deployment failed", "deployment", deploymentObjectKey.Name)
				return err
			}
		}
	}

	serviceName := fmt.Sprintf(ClusterReverseProxyService, ts.Name)
	serviceExists := true
	serviceNameObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: serviceName}

	var service = &v1.Service{}
	if err := r.Get(ctx, serviceNameObjectKey, service); err != nil {
		if apierrors.IsNotFound(err) {
			serviceExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch ingress reverse proxy service: %s", serviceName))
			return err
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
	if ts.Spec.Ingress.ClusterIssuer == nil && ts.Spec.Ingress.TLSSecretName == nil {
		return nil, fmt.Errorf("cluster issuer or tls secret name must be set, skipping ingress creation")
	}

	annotations := map[string]string{}
	var tlsSecretName string

	if ts.Spec.Ingress.ClusterIssuer != nil {
		annotations["cert-manager.io/cluster-issuer"] = *ts.Spec.Ingress.ClusterIssuer
		tlsSecretName = fmt.Sprintf("%s-reverse-proxy-%s-certificate-tls", ts.Name, *ts.Spec.Ingress.ClusterIssuer)
	}

	if ts.Spec.Ingress.Annotations != nil {
		maps.Copy(annotations, ts.Spec.Ingress.Annotations)
	}

	if ts.Spec.Ingress.TLSSecretName != nil {
		tlsSecretName = *ts.Spec.Ingress.TLSSecretName
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: getObjectMeta(ts, &key.Name, annotations),
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(ts.Spec.Ingress.IngressClassName),
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{ts.Spec.Ingress.Host},
					SecretName: tlsSecretName,
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
											Name: fmt.Sprintf(ClusterReverseProxyService, ts.Name),
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

func (r *TypesenseClusterReconciler) updateIngress(ctx context.Context, ig networkingv1.Ingress, ts *tsv1alpha1.TypesenseCluster) (*networkingv1.Ingress, error) {
	if ts.Spec.Ingress.ClusterIssuer == nil && ts.Spec.Ingress.TLSSecretName == nil {
		return nil, fmt.Errorf("cluster issuer or tls secret name must be set, keeping the current ingress in place")
	}
	patch := client.MergeFrom(ig.DeepCopy())

	ig.Spec.Rules[0].Host = ts.Spec.Ingress.Host
	ig.Spec.IngressClassName = ptr.To[string](ts.Spec.Ingress.IngressClassName)

	annotations := map[string]string{}
	var tlsSecretName string

	if ts.Spec.Ingress.ClusterIssuer != nil {
		annotations["cert-manager.io/cluster-issuer"] = *ts.Spec.Ingress.ClusterIssuer
		tlsSecretName = fmt.Sprintf("%s-reverse-proxy-%s-certificate-tls", ts.Name, *ts.Spec.Ingress.ClusterIssuer)
	}

	if ts.Spec.Ingress.Annotations != nil {
		maps.Copy(annotations, ts.Spec.Ingress.Annotations)
	}
	ig.Annotations = annotations

	if ts.Spec.Ingress.TLSSecretName != nil {
		tlsSecretName = *ts.Spec.Ingress.TLSSecretName
	}
	ig.Spec.TLS[0].SecretName = tlsSecretName

	if err := r.Patch(ctx, &ig, patch); err != nil {
		return nil, err
	}

	return &ig, nil
}

func (r *TypesenseClusterReconciler) deleteIngress(ctx context.Context, ig *networkingv1.Ingress) error {
	err := r.Delete(ctx, ig)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) getIngressAnnotations(ig *networkingv1.Ingress) map[string]string {
	annotations := make(map[string]string, len(ig.Annotations))
	for k, v := range ig.Annotations {
		annotations[k] = v
	}

	delete(annotations, "cert-manager.io/cluster-issuer")
	if len(annotations) == 0 {
		annotations = nil
	}

	return annotations
}

func (r *TypesenseClusterReconciler) createIngressConfigMap(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, ig *networkingv1.Ingress) (*v1.ConfigMap, error) {
	nginxConf, err := r.getIngressNginxConf(ts)
	if err != nil {
		return nil, err
	}

	icm := &v1.ConfigMap{
		ObjectMeta: getReverseProxyObjectMeta(ts, &key.Name, nil),
		Data: map[string]string{
			"nginx.conf": nginxConf,
		},
	}

	err = ctrl.SetControllerReference(ig, icm, r.Scheme)
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
	nginxConf, err := r.getIngressNginxConf(ts)
	if err != nil {
		return nil, err
	}

	desired := cm.DeepCopy()
	desired.Data = map[string]string{
		"nginx.conf": nginxConf,
	}

	err = r.Update(ctx, desired)
	if err != nil {
		r.logger.Error(err, "updating ingress config map failed")
		return nil, err
	}

	return desired, nil
}

func (r *TypesenseClusterReconciler) shouldUpdateIngressConfigMap(cm *v1.ConfigMap, ts *tsv1alpha1.TypesenseCluster) (bool, error) {
	nginxConf, err := r.getIngressNginxConf(ts)
	if err != nil {
		return false, err
	}

	return cm.Data["nginx.conf"] != nginxConf, nil
}

func (r *TypesenseClusterReconciler) getIngressNginxConf(ts *tsv1alpha1.TypesenseCluster) (string, error) {
	ref := ""
	if ts.Spec.Ingress != nil && ts.Spec.Ingress.Referer != nil {
		ref = fmt.Sprintf(referer, *ts.Spec.Ingress.Referer)
	}

	httpDirectives := ""
	if ts.Spec.Ingress != nil && ts.Spec.Ingress.HttpDirectives != nil {
		httpDirectives = strings.ReplaceAll(*ts.Spec.Ingress.HttpDirectives, ";", ";\n")
	}

	serverDirectives := ""
	if ts.Spec.Ingress != nil && ts.Spec.Ingress.ServerDirectives != nil {
		serverDirectives = strings.ReplaceAll(*ts.Spec.Ingress.ServerDirectives, ";", ";\n")
	}

	locationDirectives := ""
	if ts.Spec.Ingress != nil && ts.Spec.Ingress.LocationDirectives != nil {
		locationDirectives = strings.ReplaceAll(*ts.Spec.Ingress.LocationDirectives, ";", ";\n")
	}

	nginxConfData := struct {
		HttpDirectives     string
		ServerDirectives   string
		LocationDirectives string
		Referer            string
		ServiceName        string
		ServicePort        string
	}{
		HttpDirectives:     httpDirectives,
		ServerDirectives:   serverDirectives,
		LocationDirectives: locationDirectives,
		Referer:            ref,
		ServiceName:        ts.Name,
		ServicePort:        strconv.Itoa(ts.Spec.ApiPort),
	}

	tmpl, err := template.New("nginxConf").Parse(confTemplate)
	if err != nil {
		r.logger.Error(err, "error parsing template")
		return "", err
	}

	var outputBuffer bytes.Buffer
	err = tmpl.Execute(&outputBuffer, nginxConfData)
	if err != nil {
		r.logger.Error(err, "error executing template")
		return "", err
	}

	conf := outputBuffer.String()
	return conf, nil
}

func (r *TypesenseClusterReconciler) createIngressDeployment(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, ig *networkingv1.Ingress) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{
		ObjectMeta: getReverseProxyObjectMeta(ts, &key.Name, nil),
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
							Name:  fmt.Sprintf(ClusterReverseProxy, ts.Name),
							Image: "nginx:alpine",
							Ports: []v1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
							Resources: ts.Spec.Ingress.GetReverseProxyResources(),
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
										Name: fmt.Sprintf(ClusterReverseProxyConfigMap, ts.Name),
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
		ObjectMeta: getReverseProxyObjectMeta(ts, &key.Name, nil),
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
