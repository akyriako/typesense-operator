package controller

const (
	ClusterNodesConfigMap           = "%s-nodeslist"
	ClusterAdminApiKeySecret        = "%s-admin-key"
	ClusterAdminApiKeySecretKeyName = "typesense-api-key"

	ClusterHeadlessService = "%s-sts-svc"
	ClusterRestService     = "%s-svc"
	ClusterStatefulSet     = "%s-sts"
	ClusterAppLabel        = "%s-sts"

	ClusterReverseProxyAppLabel  = "%s-rp"
	ClusterReverseProxyIngress   = "%s-reverse-proxy"
	ClusterReverseProxyConfigMap = "%s-reverse-proxy-config"
	ClusterReverseProxy          = "%s-reverse-proxy"
	ClusterReverseProxyService   = "%s-reverse-proxy-svc"

	ClusterHttpRoute               = "%s-%s"
	ClusterHttpRouteReferenceGrant = "%s-%s-reference-grant"

	ClusterMetricsPodMonitorAppLabel = "%s-sts"
	ClusterMetricsPodMonitor         = "%s-podmonitor"

	ClusterScraperCronJob          = "%s-scraper"
	ClusterScraperCronJobContainer = "%s-docsearch-scraper"
)

// for operator-assigned labels (in line with SetupWithManager)
const (
	appLabelKey = "app"

	instanceLabelKey = "app.kubernetes.io/instance"

	managedByLabelKey   = "app.kubernetes.io/managed-by"
	managedByLabelValue = "typesense-operator"

	nameLabelKey   = "app.kubernetes.io/name"
	nameLabelValue = "typesense"
)

// for consistency across resources
const (
	typesenseContainerName   = "typesense"
	healthcheckContainerName = "healthcheck"

	httpPortName        = "http"
	healthcheckPortName = "healthcheck"
	metricsPortName     = "metrics"

	nodesListVolumeName   = "nodeslist"
	dataVolumeName        = "data"
	nginxConfigVolumeName = "nginx-config"

	nginxConfKey = "nginx.conf"
)

// for in-cluster traffic
const (
	typesenseProtocolEnvVar = "TYPESENSE_PROTOCOL"
	httpScheme              = "http"
)
