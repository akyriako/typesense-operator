package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"text/template"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	confTemplate = `worker_processes auto;
pid /tmp/nginx.pid;
events {
  worker_connections 1024;
}
http {
  sendfile on;
  tcp_nopush on;
  tcp_nodelay on;
  keepalive_timeout 65;
  types_hash_max_size 2048;
  server_tokens off;

  {{- if .HttpDirectives}}
  {{.HttpDirectives}}
  {{- end}}

  server {
    listen 8080;

    {{- if .Referer}}
    {{.Referer}}
    {{- end}}
    {{- if .ServerDirectives}}
    {{.ServerDirectives}}
    {{- end}}
    location / {
      proxy_pass http://{{.ServiceName}}-svc:{{.ServicePort}}/;
      proxy_http_version 1.1;
      proxy_set_header Host $host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto $scheme;
      proxy_set_header Connection "";
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

const (
	clusterIssuerAnnotationKey = "cert-manager.io/cluster-issuer"
	nginxConfKey               = "nginx.conf"
	// NginxConfigHashAnnotationKey is the annotation key for the nginx config hash.
	NginxConfigHashAnnotationKey = "typesense-operator/nginx-config-hash"
)

//nolint:gocyclo // ReconcileIngress coordinates multiple child resources
func (r *TypesenseClusterReconciler) ReconcileIngress(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (err error) {
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

	if ts.Spec.Ingress == nil || ts.Spec.HttpRoutes != nil {
		err := r.cleanupReverseProxy(ctx, ts)
		if err != nil {
			r.logger.Error(err, "failed to clean up reverse proxy resources")
		}
		return err
	}

	if !ingressExists {
		r.logger.V(debugLevel).Info("creating ingress", "ingress", ingressObjectKey.Name)

		_, err = r.createIngress(ctx, ingressObjectKey, ts)
		if err != nil {
			r.logger.Error(err, "creating ingress failed", "ingress", ingressObjectKey.Name)
			return err
		}
	} else {
		lbls := r.getIngressLabels(ig, ts, ingressObjectKey)
		anons := r.getIngressAnnotations(ig, ts)

		pathType := ts.Spec.Ingress.PathType
		if pathType == nil {
			pathType = ptr.To(networkingv1.PathTypePrefix)
		}

		if len(ig.Spec.Rules) != 1 ||
			ig.Spec.Rules[0].HTTP == nil ||
			len(ig.Spec.Rules[0].HTTP.Paths) != 1 ||
			ts.Spec.Ingress.Host != ig.Spec.Rules[0].Host ||
			(ts.Spec.Ingress.ClusterIssuer != nil && *ts.Spec.Ingress.ClusterIssuer != ig.Annotations[clusterIssuerAnnotationKey]) ||
			!apiequality.Semantic.DeepEqual(ts.Spec.Ingress.Labels, lbls) ||
			!apiequality.Semantic.DeepEqual(ts.Spec.Ingress.Annotations, anons) ||
			(ts.Spec.Ingress.TLSSecretName != nil && (len(ig.Spec.TLS) == 0 || *ts.Spec.Ingress.TLSSecretName != ig.Spec.TLS[0].SecretName)) ||
			(ts.Spec.Ingress.TLSSecretName == nil && ts.Spec.Ingress.ClusterIssuer == nil && len(ig.Spec.TLS) > 0) ||
			ig.Spec.IngressClassName == nil || ts.Spec.Ingress.IngressClassName != *ig.Spec.IngressClassName ||
			ts.Spec.Ingress.Path != ig.Spec.Rules[0].HTTP.Paths[0].Path ||
			ig.Spec.Rules[0].HTTP.Paths[0].PathType == nil || *pathType != *ig.Spec.Rules[0].HTTP.Paths[0].PathType {

			r.logger.V(debugLevel).Info("updating ingress", "ingress", ingressObjectKey.Name)

			_, err = r.updateIngress(ctx, *ig, ts)
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

	if !configMapExists {
		r.logger.V(debugLevel).Info("creating ingress config map", "configmap", configMapObjectKey.Name)

		_, err = r.createIngressConfigMap(ctx, configMapObjectKey, ts)
		if err != nil {
			r.logger.Error(err, "creating ingress config map failed", "configmap", configMapObjectKey.Name)
			return err
		}
	} else {
		shouldUpdate, err := r.shouldUpdateIngressConfigMap(cm, ts)
		if err != nil {
			return err
		}

		if shouldUpdate {
			r.logger.V(debugLevel).Info("updating ingress config map", "configmap", configMapObjectKey.Name)

			_, err = r.updateIngressConfigMap(ctx, cm, ts)
			if err != nil {
				return err
			}
		}
	}

	deploymentName := fmt.Sprintf(ClusterReverseProxy, ts.Name)

	volumes := r.getDefaultReverseProxyVolumes(ts.Name)
	volumeMounts := r.getDefaultReverseProxyVolumeMounts()
	var securityContext *v1.SecurityContext

	volumes = append(volumes, ts.Spec.Ingress.Volumes...)
	volumeMounts = append(volumeMounts, ts.Spec.Ingress.VolumeMounts...)

	if ts.Spec.Ingress.SecurityContext != nil {
		securityContext = ts.Spec.Ingress.SecurityContext
	} else if ts.Spec.Ingress.ReadOnlyRootFilesystem {
		securityContext = &v1.SecurityContext{
			ReadOnlyRootFilesystem: ptr.To(true),
		}
	}

	replicas := int32(1)
	if ts.Spec.Ingress.Replicas != nil {
		replicas = *ts.Spec.Ingress.Replicas
	}

	nginxConf, err := r.getIngressNginxConf(ts)
	if err != nil {
		return err
	}

	podAnnotations := make(map[string]string)
	podAnnotations[NginxConfigHashAnnotationKey] = fmt.Sprintf("%x", sha256.Sum256([]byte(nginxConf)))

	desiredDeployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: getReverseProxyObjectMeta(ts, &deploymentName, nil),
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: getReverseProxyLabels(ts),
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      getMergedLabels(getDefaultLabels(ts), getReverseProxyLabels(ts)),
					Annotations: podAnnotations,
				},
				Spec: v1.PodSpec{
					ImagePullSecrets: ts.Spec.ImagePullSecrets,
					Containers: []v1.Container{
						{
							Name:    fmt.Sprintf(ClusterReverseProxy, ts.Name),
							Image:   ts.Spec.Ingress.Image,
							Command: ts.Spec.Ingress.Command,
							Ports: []v1.ContainerPort{
								{
									ContainerPort: 8080,
								},
							},
							Resources:       ts.Spec.Ingress.GetReverseProxyResources(),
							VolumeMounts:    volumeMounts,
							SecurityContext: securityContext,
							LivenessProbe: &v1.Probe{
								ProbeHandler: v1.ProbeHandler{
									TCPSocket: &v1.TCPSocketAction{
										Port: intstr.IntOrString{Type: intstr.Int, IntVal: int32(8080)},
									},
								},
								InitialDelaySeconds: 5,
								TimeoutSeconds:      5,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &v1.Probe{
								ProbeHandler: v1.ProbeHandler{
									TCPSocket: &v1.TCPSocketAction{
										Port: intstr.IntOrString{Type: intstr.Int, IntVal: int32(8080)},
									},
								},
								InitialDelaySeconds: 5,
								TimeoutSeconds:      5,
								PeriodSeconds:       10,
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(ts, desiredDeployment, r.Scheme); err != nil {
		return err
	}

	//nolint:staticcheck
	if err := r.Patch(ctx, desiredDeployment, client.Apply, client.ForceOwnership, client.FieldOwner("typesense-operator")); err != nil {
		r.logger.Error(err, "updating ingress reverse proxy deployment failed", "deployment", deploymentName)
		return err
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

		_, err = r.createIngressService(ctx, serviceNameObjectKey, ts)
		if err != nil {
			r.logger.Error(err, "creating ingress reverse proxy service failed", "service", serviceNameObjectKey.Name)
			return err
		}
	} else {
		svcType := v1.ServiceTypeNodePort
		if ts.Spec.Ingress.ServiceType != "" {
			svcType = ts.Spec.Ingress.ServiceType
		}
		portsMatch := len(service.Spec.Ports) > 0 && service.Spec.Ports[0].Port == 8080 && service.Spec.Ports[0].TargetPort.IntVal == 8080

		svcAnnotations := r.getServiceAnnotations(service, ts)
		if !apiequality.Semantic.DeepEqual(svcAnnotations, ts.Spec.Ingress.ServiceAnnotations) || service.Spec.Type != svcType || !portsMatch {
			err = r.updateIngressService(ctx, service, ts)
			if err != nil {
				r.logger.Error(err, "updating ingress reverse proxy service failed", "service", serviceNameObjectKey.Name)
				return err
			}
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) cleanupReverseProxy(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	ingressName := fmt.Sprintf(ClusterReverseProxyIngress, ts.Name)
	ig := &networkingv1.Ingress{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ts.Namespace, Name: ingressName}, ig); err == nil {
		if err := r.Delete(ctx, ig); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	deployName := fmt.Sprintf(ClusterReverseProxy, ts.Name)
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ts.Namespace, Name: deployName}, deploy); err == nil {
		if err := r.Delete(ctx, deploy); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	svcName := fmt.Sprintf(ClusterReverseProxyService, ts.Name)
	svc := &v1.Service{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ts.Namespace, Name: svcName}, svc); err == nil {
		if err := r.Delete(ctx, svc); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	cmName := fmt.Sprintf(ClusterReverseProxyConfigMap, ts.Name)
	cm := &v1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ts.Namespace, Name: cmName}, cm); err == nil {
		if err := r.Delete(ctx, cm); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createIngress(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*networkingv1.Ingress, error) {
	labels := map[string]string{}
	annotations := map[string]string{}
	var tlsSecretName string

	if ts.Spec.Ingress.ClusterIssuer != nil {
		annotations[clusterIssuerAnnotationKey] = *ts.Spec.Ingress.ClusterIssuer
		tlsSecretName = fmt.Sprintf("%s-reverse-proxy-%s-certificate-tls", ts.Name, *ts.Spec.Ingress.ClusterIssuer)
	}

	if ts.Spec.Ingress.Labels != nil {
		maps.Copy(labels, ts.Spec.Ingress.Labels)
	}

	if ts.Spec.Ingress.Annotations != nil {
		maps.Copy(annotations, ts.Spec.Ingress.Annotations)
	}

	if ts.Spec.Ingress.TLSSecretName != nil {
		tlsSecretName = *ts.Spec.Ingress.TLSSecretName
	}

	var ingressTLS []networkingv1.IngressTLS
	if tlsSecretName != "" {
		ingressTLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{ts.Spec.Ingress.Host},
				SecretName: tlsSecretName,
			},
		}
	}
	// --------------------------------

	pathType := ts.Spec.Ingress.PathType
	if pathType == nil {
		pathType = ptr.To(networkingv1.PathTypePrefix)
	}

	ingress := &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: networkingv1.SchemeGroupVersion.String(),
			Kind:       "Ingress",
		},
		ObjectMeta: getIngressObjectMeta(ts, &key.Name, labels, annotations),
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(ts.Spec.Ingress.IngressClassName),
			TLS:              ingressTLS,
			Rules: []networkingv1.IngressRule{
				{
					Host: ts.Spec.Ingress.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     ts.Spec.Ingress.Path,
									PathType: pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: fmt.Sprintf(ClusterReverseProxyService, ts.Name),
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
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

	//nolint:staticcheck
	err = r.Patch(ctx, ingress, client.Apply, client.ForceOwnership, client.FieldOwner(operatorFieldOwner))
	if err != nil {
		return nil, err
	}

	return ingress, nil
}

func (r *TypesenseClusterReconciler) updateIngress(ctx context.Context, ig networkingv1.Ingress, ts *tsv1alpha1.TypesenseCluster) (*networkingv1.Ingress, error) {
	if err := r.migrateLegacyFieldOwnership(ctx, &ig); err != nil {
		return nil, err
	}

	pathType := ts.Spec.Ingress.PathType
	if pathType == nil {
		pathType = ptr.To(networkingv1.PathTypePrefix)
	}

	annotations := map[string]string{}
	if ts.Spec.Ingress.Annotations != nil {
		maps.Copy(annotations, ts.Spec.Ingress.Annotations)
	}

	labels := map[string]string{}
	if ts.Spec.Ingress.Labels != nil {
		maps.Copy(labels, ts.Spec.Ingress.Labels)
	}

	var tlsSecretName string

	if ts.Spec.Ingress.ClusterIssuer != nil {
		annotations[clusterIssuerAnnotationKey] = *ts.Spec.Ingress.ClusterIssuer
		tlsSecretName = fmt.Sprintf("%s-reverse-proxy-%s-certificate-tls", ts.Name, *ts.Spec.Ingress.ClusterIssuer)
	}

	if ts.Spec.Ingress.TLSSecretName != nil {
		tlsSecretName = *ts.Spec.Ingress.TLSSecretName
	}

	var ingressTLS []networkingv1.IngressTLS
	if tlsSecretName != "" {
		ingressTLS = []networkingv1.IngressTLS{{
			Hosts:      []string{ts.Spec.Ingress.Host},
			SecretName: tlsSecretName,
		}}
	}

	desired := &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: networkingv1.SchemeGroupVersion.String(),
			Kind:       "Ingress",
		},
		ObjectMeta: getIngressObjectMeta(ts, &ig.Name, labels, annotations),
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(ts.Spec.Ingress.IngressClassName),
			TLS:              ingressTLS,
			Rules: []networkingv1.IngressRule{
				{
					Host: ts.Spec.Ingress.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     ts.Spec.Ingress.Path,
									PathType: pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: fmt.Sprintf(ClusterReverseProxyService, ts.Name),
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
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

func (r *TypesenseClusterReconciler) getIngressLabels(ig *networkingv1.Ingress, ts *tsv1alpha1.TypesenseCluster, key client.ObjectKey) map[string]string {
	defaultLabels := getIngressObjectMeta(ts, &key.Name, nil, nil).Labels
	filters := make([]string, 0, len(defaultLabels))
	for k := range defaultLabels {
		filters = append(filters, k)
	}

	return withoutKeys(ig.Labels, filters...)
}

func (r *TypesenseClusterReconciler) getIngressAnnotations(ig *networkingv1.Ingress, ts *tsv1alpha1.TypesenseCluster) map[string]string {
	filters := append([]string{clusterIssuerAnnotationKey, rancherDomainAnnotationKey}, ts.Spec.IgnoreAnnotationsFromExternalMutations...)
	filtered := filterMap(ig.Annotations, filters...)
	return filtered
}

func (r *TypesenseClusterReconciler) createIngressConfigMap(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*v1.ConfigMap, error) {
	nginxConf, err := r.getIngressNginxConf(ts)
	if err != nil {
		return nil, err
	}

	icm := &v1.ConfigMap{
		ObjectMeta: getReverseProxyObjectMeta(ts, &key.Name, nil),
		Data: map[string]string{
			nginxConfKey: nginxConf,
		},
	}

	err = ctrl.SetControllerReference(ts, icm, r.Scheme)
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

	desired := &v1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       "ConfigMap",
		},
		ObjectMeta: getReverseProxyObjectMeta(ts, &cm.Name, nil),
		Data: map[string]string{
			nginxConfKey: nginxConf,
		},
	}

	//nolint:staticcheck
	err = r.Patch(ctx, desired, client.Apply, client.ForceOwnership, client.FieldOwner("typesense-operator"))
	if err != nil {
		r.logger.Error(err, "updating ingress config map failed")
		return nil, err
	}

	return cm, nil
}

func (r *TypesenseClusterReconciler) shouldUpdateIngressConfigMap(cm *v1.ConfigMap, ts *tsv1alpha1.TypesenseCluster) (bool, error) {
	nginxConf, err := r.getIngressNginxConf(ts)
	if err != nil {
		return false, err
	}

	return cm.Data[nginxConfKey] != nginxConf, nil
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

	templateStr := confTemplate
	if ts.Spec.Ingress != nil && ts.Spec.Ingress.Config != nil {
		templateStr = *ts.Spec.Ingress.Config
	}

	tmpl, err := template.New("nginxConf").Parse(templateStr)
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

func (r *TypesenseClusterReconciler) getDefaultReverseProxyVolumes(tsClusterName string) []v1.Volume {
	return []v1.Volume{
		{
			Name: "nginx-config",
			VolumeSource: v1.VolumeSource{
				ConfigMap: &v1.ConfigMapVolumeSource{
					LocalObjectReference: v1.LocalObjectReference{
						Name: fmt.Sprintf(ClusterReverseProxyConfigMap, tsClusterName),
					},
				},
			},
		},
	}
}

func (r *TypesenseClusterReconciler) getDefaultReverseProxyVolumeMounts() []v1.VolumeMount {
	return []v1.VolumeMount{
		{
			Name:      "nginx-config",
			MountPath: "/etc/nginx/nginx.conf",
			SubPath:   "nginx.conf",
		},
	}
}

func (r *TypesenseClusterReconciler) createIngressService(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster) (*v1.Service, error) {
	svcType := v1.ServiceTypeNodePort
	if ts.Spec.Ingress.ServiceType != "" {
		svcType = ts.Spec.Ingress.ServiceType
	}

	service := &v1.Service{
		ObjectMeta: getReverseProxyObjectMeta(ts, &key.Name, ts.Spec.Ingress.ServiceAnnotations),
		Spec: v1.ServiceSpec{
			Type:     svcType,
			Selector: getReverseProxyLabels(ts),
			Ports: []v1.ServicePort{
				{
					Protocol:   v1.ProtocolTCP,
					Port:       8080,
					TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: int32(8080)},
					Name:       protocolHttp,
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, service, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, service)
	if err != nil {
		return nil, err
	}

	return service, nil
}

func (r *TypesenseClusterReconciler) updateIngressService(ctx context.Context, service *v1.Service, ts *tsv1alpha1.TypesenseCluster) error {
	patch := client.MergeFrom(service.DeepCopy())

	svcType := v1.ServiceTypeNodePort
	if ts.Spec.Ingress.ServiceType != "" {
		svcType = ts.Spec.Ingress.ServiceType
	}

	service.Spec.Type = svcType
	service.Spec.Ports = []v1.ServicePort{
		{
			Protocol:   v1.ProtocolTCP,
			Port:       8080,
			TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: int32(8080)},
			Name:       protocolHttp,
		},
	}

	annotations := map[string]string{}
	if ts.Spec.Ingress.ServiceAnnotations != nil {
		maps.Copy(annotations, ts.Spec.Ingress.ServiceAnnotations)
	}
	filters := ts.Spec.IgnoreAnnotationsFromExternalMutations
	for k, v := range service.Annotations {
		for _, f := range filters {
			if strings.Contains(k, f) {
				annotations[k] = v
				break
			}
		}
	}
	service.Annotations = annotations

	if err := r.Patch(ctx, service, patch); err != nil {
		return err
	}

	return nil
}
