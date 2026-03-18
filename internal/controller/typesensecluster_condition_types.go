package controller

import (
	"context"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/akyriako/typesense-operator/internal/monitoring"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ConditionQuorum string

// Definitions to manage status conditions
const (
	ConditionTypeReady = "Ready"

	ConditionReasonReconciliationInProgress                              = "ReconciliationInProgress"
	ConditionReasonSecretNotReady                                        = "SecretNotReady"
	ConditionReasonConfigMapNotReady                                     = "ConfigMapNotReady"
	ConditionReasonServicesNotReady                                      = "ServicesNotReady"
	ConditionReasonIngressNotReady                                       = "IngressNotReady"
	ConditionReasonHttpRouteNotReady                                     = "HttpRouteNotReady"
	ConditionReasonScrapersNotReady                                      = "ScrapersNotReady"
	ConditionReasonMetricsExporterNotReady                               = "MetricsExporterNotReady"
	ConditionReasonQuorumStateUnknown                    ConditionQuorum = "QuorumStateUnknown"
	ConditionReasonQuorumReady                           ConditionQuorum = "QuorumReady"
	ConditionReasonQuorumNotReady                        ConditionQuorum = "QuorumNotReady"
	ConditionReasonQuorumNotReadyWaitATerm               ConditionQuorum = "QuorumNotReadyWaitATerm"
	ConditionReasonQuorumDowngraded                      ConditionQuorum = "QuorumDowngraded"
	ConditionReasonQuorumUpgraded                        ConditionQuorum = "QuorumUpgraded"
	ConditionReasonQuorumPurged                          ConditionQuorum = "QuorumPurged"
	ConditionReasonQuorumNeedsAttentionMemoryOrDiskIssue ConditionQuorum = "QuorumNeedsAttentionMemoryOrDiskIssue"
	ConditionReasonQuorumNeedsAttentionClusterIsLagging  ConditionQuorum = "QuorumNeedsAttentionClusterIsLagging"
	ConditionReasonQuorumQueuedWrites                    ConditionQuorum = "QuorumQueuedWrites"
	ConditionReasonStatefulSetNotReady                                   = "StatefulSetNotReady"

	InitReconciliationMessage = "Starting reconciliation"
	UpdateStatusMessageFailed = "failed to update typesense cluster status"
)

func (r *TypesenseClusterReconciler) initConditions(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	if len(ts.Status.Conditions) == 0 {
		if err := r.patchStatus(ctx, ts, func(status *tsv1alpha1.TypesenseClusterStatus) {
			meta.SetStatusCondition(&ts.Status.Conditions, metav1.Condition{Type: ConditionTypeReady, Status: metav1.ConditionUnknown, Reason: ConditionReasonReconciliationInProgress, Message: InitReconciliationMessage})
			status.Phase = "Bootstrapping"
		}); err != nil {
			r.logger.Error(err, UpdateStatusMessageFailed)
			return err
		}
	}
	return nil
}

func (r *TypesenseClusterReconciler) setConditionNotReady(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, reason string, err error) error {
	now := metav1.Now()
	hadOpenIncident := ts.Status.QuorumIncidentStartTime != nil

	if err := r.patchStatus(ctx, ts, func(status *tsv1alpha1.TypesenseClusterStatus) {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: ConditionTypeReady, Status: metav1.ConditionFalse, Reason: reason, Message: err.Error()})

		status.Phase = reason
		if status.QuorumIncidentStartTime == nil {
			status.QuorumIncidentStartTime = &now
		}
	}); err != nil {
		return err
	}

	cluster := ts.Name
	monitoring.QuorumHealthy.WithLabelValues(cluster).Set(0.0)

	if !hadOpenIncident {
		monitoring.QuorumIncidentsTotal.WithLabelValues(cluster).Inc()
		monitoring.QuorumIncidentStartTime.WithLabelValues(cluster).Set(float64(now.Unix()))
		monitoring.QuorumRecoveryInProgress.WithLabelValues(cluster).Set(1)
	} else {
		elapsed := now.Time.Sub(ts.Status.QuorumIncidentStartTime.Time).Seconds()
		monitoring.QuorumRecoveryInProgress.WithLabelValues(cluster).Set(elapsed)

		//r.logger.V(debugLevel).Info("recovery in progress", "elapsed", elapsed)
	}

	return nil
}

func (r *TypesenseClusterReconciler) setConditionReady(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, reason string) error {
	now := time.Now()

	recoveryDurationSeconds := 0.0
	hadOpenIncident := ts.Status.QuorumIncidentStartTime != nil

	if hadOpenIncident {
		recoveryDurationSeconds = now.Sub(ts.Status.QuorumIncidentStartTime.Time).Seconds()
		if recoveryDurationSeconds < 0 {
			recoveryDurationSeconds = 0
		}
	}

	if err := r.patchStatus(ctx, ts, func(status *tsv1alpha1.TypesenseClusterStatus) {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: ConditionTypeReady, Status: metav1.ConditionTrue, Reason: reason, Message: "Cluster is Ready"})

		status.Phase = reason
		status.QuorumIncidentStartTime = nil
	}); err != nil {
		return err
	}

	cluster := ts.Name
	monitoring.QuorumHealthy.WithLabelValues(cluster).Set(1)
	monitoring.QuorumIncidentStartTime.WithLabelValues(cluster).Set(0)
	monitoring.QuorumRecoveryInProgress.WithLabelValues(cluster).Set(0)

	if hadOpenIncident {
		monitoring.QuorumRecoveryDuration.WithLabelValues(cluster).Observe(recoveryDurationSeconds)
		//r.logger.V(debugLevel).Info("recovery completed", "elapsed", recoveryDurationSeconds)
	}

	return nil
}

func (r *TypesenseClusterReconciler) getConditionReady(ts *tsv1alpha1.TypesenseCluster) *metav1.Condition {
	condition := ts.Status.Conditions[0]
	if condition.Type != ConditionTypeReady {
		return nil
	}

	return &condition
}

func (r *TypesenseClusterReconciler) syncQuorumMetrics(ts *tsv1alpha1.TypesenseCluster) {
	cluster := ts.Name

	if ts.Status.QuorumIncidentStartTime != nil {
		elapsed := time.Since(ts.Status.QuorumIncidentStartTime.Time).Seconds()
		if elapsed < 1 {
			elapsed = 1
		}
		monitoring.QuorumHealthy.WithLabelValues(cluster).Set(0)
		monitoring.QuorumRecoveryInProgress.WithLabelValues(cluster).Set(elapsed)
		monitoring.QuorumIncidentStartTime.WithLabelValues(cluster).Set(float64(ts.Status.QuorumIncidentStartTime.Unix()))
	} else {
		monitoring.QuorumHealthy.WithLabelValues(cluster).Set(1)
		monitoring.QuorumRecoveryInProgress.WithLabelValues(cluster).Set(0)
		monitoring.QuorumIncidentStartTime.WithLabelValues(cluster).Set(0)
	}
}
