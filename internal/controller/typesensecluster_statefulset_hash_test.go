package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
)

var _ = Describe("StatefulSet replicas", func() {
	DescribeTable("are scaled to spec.replicas unless ReconcileQuorum holds the scale up",
		func(reason ConditionQuorum, replicas, desiredReplicas int, scale bool) {
			ts := &tsv1alpha1.TypesenseCluster{}
			ts.Spec.Replicas = int32(desiredReplicas)
			ts.Status.Conditions = []metav1.Condition{{Type: ConditionTypeReady, Reason: string(reason)}}

			sts := &appsv1.StatefulSet{}
			sts.Spec.Replicas = ptr.To(int32(replicas))
			sts.Spec.Template.Spec.SecurityContext = ts.Spec.GetPodSecurityContext()
			sts.Spec.Template.Spec.Containers = []corev1.Container{
				{Resources: ts.Spec.GetResources(), SecurityContext: ts.Spec.GetTypesenseSecurityContext()},
				{SecurityContext: ts.Spec.GetMetricsSecurityContext()},
				{SecurityContext: ts.Spec.GetHealthcheckSecurityContext()},
			}

			r := &TypesenseClusterReconciler{}
			_, scaleOnly, _ := r.shouldUpdateStatefulSet(sts, sts.DeepCopy(), ts)
			Expect(scaleOnly).To(Equal(scale))
			Expect(r.shouldEmergencyUpdateStatefulSet(sts, ts)).To(Equal(scale))
		},
		Entry("when the quorum is ready", ConditionReasonQuorumReady, 3, 5, true),
		Entry("after a downgrade", ConditionReasonQuorumDowngraded, 1, 3, false),
		Entry("while waiting a term after a downgrade", ConditionReasonQuorumNotReadyWaitATerm, 1, 3, false),
		Entry("after an incremental upgrade", ConditionReasonQuorumUpgraded, 2, 3, false),
		Entry("while writes are queued", ConditionReasonQuorumQueuedWrites, 1, 3, false),
		Entry("while the cluster is lagging", ConditionReasonQuorumNeedsAttentionClusterIsLagging, 2, 3, false),
		Entry("when scaling down while writes are queued", ConditionReasonQuorumQueuedWrites, 3, 1, true),
		Entry("when scaling down while the quorum is not ready", ConditionReasonQuorumNotReady, 3, 1, true),
		Entry("when scaling up from zero", ConditionReasonQuorumNotReadyWaitATerm, 0, 3, true),
	)
})
