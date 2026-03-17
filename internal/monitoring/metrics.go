package monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const metricLabelTypesenseCluster = "typesense_cluster"

var (
	QuorumRecoveryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "typesense_cluster_quorum_recovery_duration_seconds",
			Help:    "Time taken for a Typesense cluster to recover quorum after first being observed unhealthy.",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 900, 1800, 3600},
		},
		[]string{metricLabelTypesenseCluster},
	)

	QuorumHealthy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "typesense_cluster_quorum_healthy",
			Help: "Whether the Typesense cluster quorum is currently healthy (1) or unhealthy (0).",
		},
		[]string{metricLabelTypesenseCluster},
	)

	QuorumIncidentsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "typesense_cluster_quorum_incidents_total",
			Help: "Total number of quorum incidents observed for a Typesense cluster.",
		},
		[]string{metricLabelTypesenseCluster},
	)

	QuorumIncidentStartTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "typesense_cluster_quorum_incident_start_time_seconds",
			Help: "Unix timestamp when the current quorum incident started, or 0 if no incident is active.",
		},
		[]string{metricLabelTypesenseCluster},
	)

	QuorumRecoveryInProgress = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "typesense_cluster_quorum_recovery_in_progress_seconds",
			Help: "Seconds elapsed since the current quorum incident started, or 0 if quorum is healthy.",
		},
		[]string{metricLabelTypesenseCluster},
	)
)

func init() {
	metrics.Registry.MustRegister(
		QuorumRecoveryDuration,
		QuorumHealthy,
		QuorumIncidentsTotal,
		QuorumIncidentStartTime,
		QuorumRecoveryInProgress)
}
