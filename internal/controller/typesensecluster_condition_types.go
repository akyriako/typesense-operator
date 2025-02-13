package controller

import (
	"context"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ConditionQuorum string

// Definitions to manage status conditions
const (
	ConditionTypeReady = "Ready"

	ConditionReasonReconciliationInProgress                 = "ReconciliationInProgress"
	ConditionReasonSecretNotReady                           = "SecretNotReady"
	ConditionReasonConfigMapNotReady                        = "ConfigMapNotReady"
	ConditionReasonServicesNotReady                         = "ServicesNotReady"
	ConditionReasonIngressNotReady                          = "IngressNotReady"
	ConditionReasonScrapersNotReady                         = "ScrapersNotReady"
	ConditionReasonMetricsExporterNotReady                  = "MetricsExporterNotReady"
	ConditionReasonQuorumReady              ConditionQuorum = "QuorumReady"
	ConditionReasonQuorumNotReady           ConditionQuorum = "QuorumNotReady"
	ConditionReasonQuorumDowngraded         ConditionQuorum = "QuorumDowngraded"
	ConditionReasonQuorumUpgraded           ConditionQuorum = "QuorumUpgraded"
	ConditionReasonQuorumNeedsAttention     ConditionQuorum = "QuorumNeedsAttention"
	ConditionReasonStatefulSetNotReady                      = "StatefulSetNotReady"

	InitReconciliationMessage = "Starting reconciliation"
	UpdateStatusMessageFailed = "failed to update typesense cluster status"
)

func (r *TypesenseClusterReconciler) initConditions(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	if ts.Status.Conditions == nil || len(ts.Status.Conditions) == 0 {
		if err := r.patchStatus(ctx, ts, func(status *tsv1alpha1.TypesenseClusterStatus) {
			meta.SetStatusCondition(&ts.Status.Conditions, metav1.Condition{Type: ConditionTypeReady, Status: metav1.ConditionUnknown, Reason: ConditionReasonReconciliationInProgress, Message: InitReconciliationMessage})
		}); err != nil {
			r.logger.Error(err, UpdateStatusMessageFailed)
			return err
		}
	}
	return nil
}

func (r *TypesenseClusterReconciler) setConditionNotReady(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, reason string, err error) error {
	if err := r.patchStatus(ctx, ts, func(status *tsv1alpha1.TypesenseClusterStatus) {
		meta.SetStatusCondition(&ts.Status.Conditions, metav1.Condition{Type: ConditionTypeReady, Status: metav1.ConditionFalse, Reason: reason, Message: err.Error()})
	}); err != nil {
		return err
	}
	return nil
}

func (r *TypesenseClusterReconciler) setConditionReady(ctx context.Context, ts *tsv1alpha1.TypesenseCluster, reason string) error {
	if err := r.patchStatus(ctx, ts, func(status *tsv1alpha1.TypesenseClusterStatus) {
		meta.SetStatusCondition(&ts.Status.Conditions, metav1.Condition{Type: ConditionTypeReady, Status: metav1.ConditionTrue, Reason: reason, Message: "Cluster is Ready"})
	}); err != nil {
		return err
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
