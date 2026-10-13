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

	ClusterHttpRoute = "%s-%s"
	// ReferenceGrants live in the Gateway's namespace, so the cluster's namespace is part of the name
	// to avoid collisions between equally named clusters in different namespaces.
	ClusterHttpRouteReferenceGrant = "%s-%s-%s-reference-grant"

	ClusterMetricsPodMonitorAppLabel = "%s-sts"
	ClusterMetricsPodMonitor         = "%s-podmonitor"

	ClusterScraperCronJob          = "%s-scraper-%s"
	ClusterScraperCronJobContainer = "%s-%s-docsearch-scraper"
)
