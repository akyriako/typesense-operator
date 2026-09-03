package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func partitionedStatefulSet(replicas, partition, ready, updated int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "typesense-sts", Namespace: "typesense"},
		Spec: appsv1.StatefulSetSpec{
			Replicas:       ptr.To[int32](replicas),
			UpdateStrategy: rollingUpdateWithPartition(partition),
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas:   ready,
			UpdatedReplicas: updated,
		},
	}
}

func TestAdvanceRollingUpdatePartition(t *testing.T) {
	tests := []struct {
		name                                string
		replicas, partition, ready, updated int32
		want                                int32
	}{
		{"lowers the partition once the replaced member has rejoined", 3, 2, 3, 1, 1},
		{"holds while a member is still outside the quorum", 3, 2, 2, 1, 2},
		{"holds until the pods above the partition carry the new revision", 3, 2, 3, 0, 2},
		{"stops at zero", 3, 0, 3, 3, 0},
		{"single member clusters are never partitioned", 1, 0, 1, 1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sts := partitionedStatefulSet(tt.replicas, tt.partition, tt.ready, tt.updated)
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(sts).Build()
			r := &TypesenseClusterReconciler{Client: c, logger: logf.Log}

			if err := r.advanceRollingUpdatePartition(context.Background(), sts); err != nil {
				t.Fatalf("advanceRollingUpdatePartition: %v", err)
			}

			var got appsv1.StatefulSet
			key := client.ObjectKey{Name: "typesense-sts", Namespace: "typesense"}
			if err := c.Get(context.Background(), key, &got); err != nil {
				t.Fatalf("get statefulset: %v", err)
			}

			if partition := rollingUpdatePartition(&got); partition != tt.want {
				t.Errorf("partition = %d, want %d", partition, tt.want)
			}
		})
	}
}

func TestRollingUpdatePartitionDefaultsToZero(t *testing.T) {
	sts := &appsv1.StatefulSet{}
	if partition := rollingUpdatePartition(sts); partition != 0 {
		t.Errorf("partition = %d, want 0 when no rolling update strategy is set", partition)
	}
}
