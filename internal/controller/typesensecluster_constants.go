package controller

const (
	ClusterNodesConfigMap           = "%s-nodeslist"
	ClusterAdminApiKeySecret        = "%s-admin-key"
	ClusterAdminApiKeySecretKeyName = "typesense-api-key"
	ClusterApiKeySecretKeyName      = "typesense-api-key"
	ClusterApiKeySecretIdName       = "typesense-api-key-id"

	ClusterHeadlessService = "%s-sts-svc"
	ClusterRestService     = "%s-svc"
	ClusterStatefulSet     = "%s-sts"
	ClusterAppLabel        = "%s-sts"

	ClusterReverseProxyAppLabel  = "%s-rp"
	ClusterReverseProxyIngress   = "%s-reverse-proxy"
	ClusterReverseProxyConfigMap = "%s-reverse-proxy-config"
	ClusterReverseProxy          = "%s-reverse-proxy"
	ClusterReverseProxyService   = "%s-reverse-proxy-svc"

	ClusterPrometheusExporterAppLabel       = "%s-prometheus-exporter"
	ClusterPrometheusExporterDeployment     = "%s-prometheus-exporter"
	ClusterPrometheusExporterService        = "%s-prometheus-exporter-svc"
	ClusterPrometheusExporterServiceMonitor = "%s-prometheus-exporter-servicemonitor"

	ClusterScraperCronJob          = "%s-scraper"
	ClusterScraperCronJobContainer = "%s-docsearch-scraper"
)
