/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akyriako/typesense-operator/internal/version"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"k8s.io/client-go/discovery"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/akyriako/typesense-operator/internal/cert"
	"github.com/akyriako/typesense-operator/internal/controller"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	webhookSecretName           = "typesense-operator-webhook-cert"
	defaultWebhookServiceName   = "typesense-operator-webhook-service"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(gatewayv1.SchemeBuilder.AddToScheme(scheme))
	utilruntime.Must(gatewayv1beta1.SchemeBuilder.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	utilruntime.Must(discoveryv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	utilruntime.Must(tsv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var watchNamespace string
	var watchCurrentNamespace bool
	var enableWebhook bool
	var maxConcurrentReconciles int
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&watchNamespace, "watch-namespace", "",
		"Optional namespace to watch. If set, the operator is restricted to that namespace.")
	flag.BoolVar(&watchCurrentNamespace, "watch-current-namespace", false,
		"If set, the operator watches only the namespace where its Pod is running.")
	flag.BoolVar(&enableWebhook, "enable-webhook", true,
		"If set to true, the webhook server is enabled. Use --enable-webhook=false to disable it.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 3,
		"Number of TypesenseClusters reconciled in parallel. A slow or unreachable cluster then does not delay the others.")

	opts := zap.Options{
		Development:     true,
		TimeEncoder:     zapcore.ISO8601TimeEncoder,
		StacktraceLevel: zapcore.DPanicLevel,
	}

	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	watchNamespace = getWatchNamespace(watchNamespace, watchCurrentNamespace)
	if watchNamespace != "" {
		setupLog.Info("Enabling namespace-scoped mode", "watch-namespace", watchNamespace)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog = ctrl.Log.WithName("setup")

	if enableWebhook && !enableLeaderElection {
		setupLog.Info("WARNING: webhook certificate management without leader election may cause race conditions")
	}

	kubeConfig := ctrl.GetConfigOrDie()
	clientSet, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		setupLog.Error(err, "unable to create kubernetes clientset")
		os.Exit(1)
	}

	// Get Context tied to OS signals (SIGTERM, SIGINT) early to pass to background workers
	ctx := ctrl.SetupSignalHandler()
	webhookCertPath = initWebhookCertificates(
		ctx, enableWebhook, webhookCertPath, webhookCertName, webhookCertKey, clientSet,
	)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	var metricsCertWatcher *certwatcher.CertWatcher
	webhookCertWatcher, webhookTLSOpts := initWebhookCertWatcher(
		enableWebhook, webhookCertPath, webhookCertName, webhookCertKey, tlsOpts,
	)

	var webhookServer webhook.Server
	if enableWebhook {
		webhookServer = webhook.NewServer(webhook.Options{
			TLSOpts: webhookTLSOpts,
		})
	}

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		// TODO(user): TLSOpts is used to allow configuring the TLS config used for the server. If certificates are
		// not provided, self-signed certificates will be generated by default. This option is not recommended for
		// production environments as self-signed certificates do not offer the same level of trust and security
		// as certificates issued by a trusted Certificate Authority (CA). The primary risk is potentially allowing
		// unauthorized access to sensitive metrics data. Consider replacing with CertDir, CertName, and KeyName
		// to provide certificates, ensuring the server communicates using trusted and secure certificates.
		TLSOpts: tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	leaderElectionID := "9cf0818f.opentelekomcloud.com"
	if watchNamespace != "" {
		leaderElectionID = fmt.Sprintf("%s.%s", watchNamespace, leaderElectionID)
	}

	options := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       leaderElectionID,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	}

	if enableWebhook {
		options.WebhookServer = webhookServer
	}

	if watchNamespace != "" {
		options.Cache = getNamespacedCacheOptions(watchNamespace, enableWebhook)
	}

	mgr, err := ctrl.NewManager(kubeConfig, options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(kubeConfig)
	if err != nil {
		setupLog.Error(err, "unable to create discovery client")
		os.Exit(1)
	}

	if err = (&controller.TypesenseClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		//nolint:staticcheck // GetEventRecorderFor is deprecated but required for backward compatibility
		Recorder:                mgr.GetEventRecorderFor("typesensecluster-controller"),
		ClientSet:               clientSet,
		DiscoveryClient:         discoveryClient,
		Configuration:           mgr.GetConfig(),
		InCluster:               isInCluster(),
		HttpClient:              mgr.GetHTTPClient(),
		MaxConcurrentReconciles: maxConcurrentReconciles,
		WatchNamespace:          watchNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TypesenseCluster")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if enableWebhook {
		setupLog.Info("Setting up webhook for TypesenseCluster")
		if err = (&tsv1alpha1.TypesenseCluster{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "TypesenseCluster")
			os.Exit(1)
		}
	}

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "Unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if enableWebhook && webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "Unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	if enableWebhook && isInCluster() {
		setupLog.Info("Adding webhook secret reconciler to manager")
		if err := (&WebhookSecretReconciler{
			Client:      mgr.GetClient(),
			ClientSet:   clientSet,
			CertDir:     webhookCertPath,
			CertName:    webhookCertName,
			KeyName:     webhookCertKey,
			Namespace:   getOperatorNamespace(),
			SecretName:  webhookSecretName,
			ServiceName: getWebhookServiceName(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Unable to add webhook secret reconciler to manager")
			os.Exit(1)
		}
	}

	addHealthChecks(mgr, webhookServer)

	v := version.GetBuildInfo()
	setupLog.Info("starting manager",
		"version", fmt.Sprintf("%s+%s", v.Version, v.Commit), "buildDate", v.BuildDate, "goVersion", v.GoVersion)
	// ctx already holds the signal handler; SetupSignalHandler panics when called a second time.
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type WebhookSecretReconciler struct {
	client.Client
	ClientSet   *kubernetes.Clientset
	CertDir     string
	CertName    string
	KeyName     string
	Namespace   string
	SecretName  string
	ServiceName string
}

func (r *WebhookSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, req.NamespacedName, &secret); err != nil {
		if k8serrors.IsNotFound(err) {
			setupLog.Info("Webhook secret deleted, regenerating...")
			err = setupWebhookCertificates(
				ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
				r.Namespace, r.ServiceName, r.SecretName,
			)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	certData, ok := secret.Data["tls.crt"]
	if !ok {
		setupLog.Info("Webhook secret missing tls.crt, regenerating...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}

	block, _ := pem.Decode(certData)
	if block == nil {
		setupLog.Info("Webhook secret tls.crt is invalid, regenerating...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}
	certParsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		setupLog.Info("Webhook secret tls.crt cannot be parsed, regenerating...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}

	var caParsed *x509.Certificate
	caData, ok := secret.Data["ca.crt"]
	if !ok {
		setupLog.Info("Webhook secret missing ca.crt, regenerating...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}
	caBlock, _ := pem.Decode(caData)
	if caBlock == nil {
		setupLog.Info("Webhook secret ca.crt is invalid, regenerating...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}
	caParsed, err = x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		setupLog.Info("Webhook secret ca.crt cannot be parsed, regenerating...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}

	timeRemaining := time.Until(certParsed.NotAfter)
	caTimeRemaining := time.Until(caParsed.NotAfter)

	if timeRemaining < 30*24*time.Hour || caTimeRemaining < 6*30*24*time.Hour {
		setupLog.Info("Webhook certificates are nearing expiration, rotating now...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}

	// Another replica may have rotated the certificate; every replica serves the webhook, so each one
	// has to pick up the current certificate from the secret.
	keyData, ok := secret.Data["tls.key"]
	if !ok {
		setupLog.Info("Webhook secret missing tls.key, regenerating...")
		err := setupWebhookCertificates(
			ctx, r.ClientSet, r.CertDir, r.CertName, r.KeyName,
			r.Namespace, r.ServiceName, r.SecretName,
		)
		return ctrl.Result{}, err
	}

	if err := syncCertificatesToDisk(r.CertDir, r.CertName, r.KeyName, certData, keyData); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := timeRemaining - (30 * 24 * time.Hour)
	caRequeueAfter := caTimeRemaining - (6 * 30 * 24 * time.Hour)
	if caRequeueAfter < requeueAfter {
		requeueAfter = caRequeueAfter
	}
	if requeueAfter < 0 {
		requeueAfter = 10 * time.Second
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *WebhookSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("webhook-certificate").
		For(&corev1.Secret{}).
		// Every replica serves webhook requests, so non-leaders must keep their on-disk certificate in sync
		// with the secret as well. Concurrent rotations are resolved by the optimistic locking in
		// ensureCertificatesInSecret.
		WithOptions(crcontroller.Options{NeedLeaderElection: ptr.To(false)}).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetName() == r.SecretName && object.GetNamespace() == r.Namespace
		})).
		Complete(r)
}

func initWebhookCertificates(
	ctx context.Context, enableWebhook bool, certPath, certName, certKey string, clientSet *kubernetes.Clientset,
) string {
	if !enableWebhook {
		return certPath
	}

	// Note: In secure environments configured with `readOnlyRootFilesystem: true`,
	// the directory /tmp/k8s-webhook-server/serving-certs must be explicitly mounted
	// as an `emptyDir` volume inside the Operator Deployment/Helm Chart to prevent crashloops.
	// Automatically set the default webhook certificate path when running in-cluster
	if certPath == "" && isInCluster() {
		certPath = "/tmp/k8s-webhook-server/serving-certs"
	}

	// Generate and patch self-signed certificates dynamically
	if isInCluster() {
		setupLog.Info("Generating self-signed webhook certificates")
		if err := setupWebhookCertificates(
			ctx, clientSet, certPath, certName, certKey,
			getOperatorNamespace(), getWebhookServiceName(), webhookSecretName,
		); err != nil {
			setupLog.Error(err, "failed to setup webhook certificates")
			os.Exit(1)
		}
	}
	return certPath
}

func initWebhookCertWatcher(
	enableWebhook bool, certPath, certName, certKey string, tlsOpts []func(*tls.Config),
) (*certwatcher.CertWatcher, []func(*tls.Config)) {
	if !enableWebhook || len(certPath) == 0 {
		return nil, tlsOpts
	}
	setupLog.Info("Initializing webhook certificate watcher using provided certificates",
		"webhook-cert-path", certPath, "webhook-cert-name", certName, "webhook-cert-key", certKey)

	watcher, err := certwatcher.New(
		filepath.Join(certPath, certName),
		filepath.Join(certPath, certKey),
	)
	if err != nil {
		setupLog.Error(err, "Failed to initialize webhook certificate watcher")
		os.Exit(1)
	}

	tlsOpts = append(tlsOpts, func(config *tls.Config) {
		config.GetCertificate = watcher.GetCertificate
	})
	return watcher, tlsOpts
}

func isInCluster() bool {
	const (
		tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	)

	_, err := os.Stat(tokenFile)
	inCluster := err == nil
	if !inCluster {
		setupLog.V(1).Info("manager is running in out-of-cluster mode")
	}

	return inCluster
}

// addHealthChecks registers the liveness and readiness checks. webhookServer is nil when webhooks are disabled.
func addHealthChecks(mgr ctrl.Manager, webhookServer webhook.Server) {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Only receive webhook traffic once the webhook server serves; with failurePolicy Ignore, requests that
	// reach a pod that is not listening yet would otherwise be admitted without validation.
	if webhookServer != nil {
		if err := mgr.AddReadyzCheck("webhook", webhookServer.StartedChecker()); err != nil {
			setupLog.Error(err, "unable to set up webhook ready check")
			os.Exit(1)
		}
	}
}

// getOperatorNamespace returns the namespace the operator pod runs in, or "" when running out of cluster.
func getOperatorNamespace() string {
	content, err := os.ReadFile(serviceAccountNamespaceFile)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(content))
}

func getNamespacedCacheOptions(watchNamespace string, enableWebhook bool) cache.Options {
	options := cache.Options{
		DefaultNamespaces: map[string]cache.Config{
			watchNamespace: {},
		},
	}

	// The webhook certificate secret lives in the operator's own namespace. When that namespace is not
	// the watched one (gapped mode), the webhook secret reconciler would never see it, so the cache also
	// watches that single secret there.
	operatorNamespace := getOperatorNamespace()
	if !enableWebhook || operatorNamespace == "" || operatorNamespace == watchNamespace {
		return options
	}

	options.ByObject = map[client.Object]cache.ByObject{
		&corev1.Secret{}: {
			Namespaces: map[string]cache.Config{
				watchNamespace: {},
				operatorNamespace: {
					FieldSelector: fields.OneTermEqualSelector("metadata.name", webhookSecretName),
				},
			},
		},
	}

	return options
}

func getWebhookServiceName() string {
	if svcName := os.Getenv("WEBHOOK_SERVICE_NAME"); svcName != "" {
		return svcName
	}

	return defaultWebhookServiceName
}

func getWatchNamespace(flagValue string, useCurrent bool) string {
	if !useCurrent {
		useCurrent = strings.EqualFold(strings.TrimSpace(os.Getenv("WATCH_CURRENT_NAMESPACE")), "true")
	}

	if useCurrent {
		content, err := os.ReadFile(serviceAccountNamespaceFile)
		if err != nil {
			setupLog.Error(err, "unable to read current namespace from service account")
			os.Exit(1)
		}
		return strings.TrimSpace(string(content))
	}

	if strings.TrimSpace(flagValue) == "" {
		flagValue = strings.TrimSpace(os.Getenv("WATCH_NAMESPACE"))
	}

	return strings.TrimSpace(flagValue)
}

func setupWebhookCertificates(
	ctx context.Context, clientSet *kubernetes.Clientset,
	certDir, certName, keyName, namespace, svcName, secretName string,
) error {
	caPEM, certPEM, keyPEM, err := ensureCertificatesInSecret(ctx, clientSet, namespace, svcName, secretName)
	if err != nil {
		return fmt.Errorf("failed to ensure certificates in secret: %w", err)
	}

	if err := writeCertificatesToDisk(certDir, certName, keyName, certPEM, keyPEM); err != nil {
		return fmt.Errorf("failed to write certificates to disk: %w", err)
	}

	if err := patchWebhookConfiguration(ctx, clientSet, namespace, svcName, caPEM); err != nil {
		return fmt.Errorf("failed to patch webhook configuration: %w", err)
	}

	return nil
}

func ensureCertificatesInSecret(
	ctx context.Context, clientSet *kubernetes.Clientset, namespace, svcName, secretName string,
) ([]byte, []byte, []byte, error) {
	var caPEM, certPEM, keyPEM, caKeyPEM []byte

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, getErr := clientSet.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		exists := getErr == nil

		if getErr != nil && !k8serrors.IsNotFound(getErr) {
			return getErr
		}

		needsGeneration := true
		if exists && secret != nil {
			caPEM = secret.Data["ca.crt"]
			caKeyPEM = secret.Data["ca.key"]
			if certData, ok := secret.Data["tls.crt"]; ok {
				block, _ := pem.Decode(certData)
				if block != nil {
					parsed, err := x509.ParseCertificate(block.Bytes)
					caValid := false
					if caBlock, _ := pem.Decode(caPEM); caBlock != nil {
						if caParsed, err := x509.ParseCertificate(caBlock.Bytes); err == nil {
							if time.Until(caParsed.NotAfter) > 6*30*24*time.Hour {
								caValid = true
							}
						}
					}

					if err == nil && time.Until(parsed.NotAfter) > 30*24*time.Hour && caValid {
						expectedDNSNames := cert.GetWebhookSANs(svcName, namespace)
						hasAllSANs := true
						for _, expected := range expectedDNSNames {
							found := false
							for _, dns := range parsed.DNSNames {
								if dns == expected {
									found = true
									break
								}
							}
							if !found {
								hasAllSANs = false
								break
							}
						}
						if hasAllSANs {
							needsGeneration = false
							certPEM = secret.Data["tls.crt"]
							keyPEM = secret.Data["tls.key"]
						}
					}
				}
			}
		}

		if !needsGeneration {
			setupLog.Info("Loaded valid webhook certificates from Secret", "secret", secretName)
			return nil
		}

		setupLog.Info("Generating new webhook certificates")
		var err error
		caPEM, certPEM, keyPEM, caKeyPEM, err = cert.GenerateWebhookCerts(svcName, namespace, caPEM, caKeyPEM)
		if err != nil {
			return fmt.Errorf("failed to generate certs: %w", err)
		}

		secretData := map[string][]byte{
			"ca.crt":  caPEM,
			"ca.key":  caKeyPEM,
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		}

		if !exists {
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
				Type:       corev1.SecretTypeTLS,
				Data:       secretData,
			}
			_, err = clientSet.CoreV1().Secrets(namespace).Create(ctx, newSecret, metav1.CreateOptions{})
			if k8serrors.IsAlreadyExists(err) {
				return k8serrors.NewConflict(corev1.Resource("secrets"), secretName, err)
			}
			return err
		}

		secret.Data = secretData
		secret.Type = corev1.SecretTypeTLS
		_, err = clientSet.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		setupLog.Error(err, "Warning: failed to persist webhook certs to secret (RBAC missing?)")
	}

	return caPEM, certPEM, keyPEM, err
}

func writeCertificatesToDisk(certDir, certName, keyName string, certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return fmt.Errorf("failed to create cert dir: %w", err)
	}

	certPath := filepath.Join(certDir, certName)
	if err := os.WriteFile(certPath+".tmp", certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write tmp cert: %w", err)
	}
	if err := os.Rename(certPath+".tmp", certPath); err != nil {
		return fmt.Errorf("failed to atomically rename cert: %w", err)
	}

	keyPath := filepath.Join(certDir, keyName)
	if err := os.WriteFile(keyPath+".tmp", keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write tmp key: %w", err)
	}
	if err := os.Rename(keyPath+".tmp", keyPath); err != nil {
		return fmt.Errorf("failed to atomically rename key: %w", err)
	}

	return nil
}

// syncCertificatesToDisk writes the certificate and key only when they differ from what is already on disk.
func syncCertificatesToDisk(certDir, certName, keyName string, certPEM, keyPEM []byte) error {
	currentCert, certErr := os.ReadFile(filepath.Join(certDir, certName))
	currentKey, keyErr := os.ReadFile(filepath.Join(certDir, keyName))
	if certErr == nil && keyErr == nil && bytes.Equal(currentCert, certPEM) && bytes.Equal(currentKey, keyPEM) {
		return nil
	}

	setupLog.Info("Webhook certificate on disk differs from secret, updating")
	return writeCertificatesToDisk(certDir, certName, keyName, certPEM, keyPEM)
}

func patchWebhookConfiguration(
	ctx context.Context, clientSet *kubernetes.Clientset, namespace, svcName string, caPEM []byte,
) error {
	// Patch webhook config with retry logic
	for i := 0; i < 5; i++ {
		webhooks, err := clientSet.AdmissionregistrationV1().
			ValidatingWebhookConfigurations().
			List(ctx, metav1.ListOptions{})
		if err != nil {
			if k8serrors.IsForbidden(err) {
				setupLog.Info(
					"Warning: Insufficient permissions to list " +
						"ValidatingWebhookConfigurations. Skipping webhook certificate " +
						"injection (expected in gapped mode).",
				)
				return nil
			}
			return fmt.Errorf("failed to list ValidatingWebhookConfigurations: %w", err)
		}

		var targetWebhookName string
		for _, wh := range webhooks.Items {
			for _, w := range wh.Webhooks {
				if w.ClientConfig.Service != nil &&
					w.ClientConfig.Service.Name == svcName &&
					w.ClientConfig.Service.Namespace == namespace {
					targetWebhookName = wh.Name
					break
				}
			}
			if targetWebhookName != "" {
				break
			}
		}

		if targetWebhookName != "" {
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				wh, err := clientSet.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(
					ctx, targetWebhookName, metav1.GetOptions{},
				)
				if err != nil {
					return err
				}

				updated := false
				for j, w := range wh.Webhooks {
					if w.ClientConfig.Service != nil &&
						w.ClientConfig.Service.Name == svcName &&
						w.ClientConfig.Service.Namespace == namespace {
						if !bytes.Equal(w.ClientConfig.CABundle, caPEM) {
							wh.Webhooks[j].ClientConfig.CABundle = caPEM
							updated = true
						}
					}
				}

				if !updated {
					return nil // Already up to date
				}

				_, err = clientSet.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(
					ctx, wh, metav1.UpdateOptions{},
				)
				return err
			})

			if err != nil {
				return fmt.Errorf("failed to update ValidatingWebhookConfiguration %s: %w", targetWebhookName, err)
			}

			setupLog.Info("Successfully patched ValidatingWebhookConfiguration", "name", targetWebhookName)
			return nil
		}

		delay := time.Duration(1<<i)*time.Second + time.Duration(rand.Intn(1000))*time.Millisecond
		setupLog.Info("ValidatingWebhookConfiguration not found yet, retrying...", "delay", delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	setupLog.Info("Warning: No matching ValidatingWebhookConfiguration found to patch.")
	return nil
}
