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
