package controller

import (
	"context"
	"fmt"
	"testing"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestIsSafeToEmergencyUpdate(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name           string
		currentImage   string
		desiredImage   string
		pods           []*corev1.Pod
		withListError  bool
		expectedResult bool
		description    string
	}{
		{
			name:           "image mismatch blocks emergency update",
			currentImage:   "typesense/typesense:0.24.0",
			desiredImage:   "typesense/typesense:0.25.0",
			pods:           []*corev1.Pod{},
			expectedResult: false,
			description:    "Emergency updates should be blocked when the desired image differs from the current image",
		},
		{
			name:           "no pods allows emergency update",
			currentImage:   "typesense/typesense:0.24.0",
			desiredImage:   "typesense/typesense:0.24.0",
			pods:           []*corev1.Pod{},
			expectedResult: true,
			description:    "Emergency updates should be allowed when no pods exist yet",
		},
		{
			name:         "all pods unschedulable due to insufficient CPU",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient cpu.",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "Emergency updates should be allowed when all pods are unschedulable due to insufficient CPU",
		},
		{
			name:         "all pods unschedulable due to insufficient memory",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient memory.",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "Emergency updates should be allowed when all pods are unschedulable due to insufficient memory",
		},
		{
			name:         "multiple pods all unschedulable",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient cpu.",
					},
				}, nil),
				createPod("test-sts-1", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient memory.",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "Emergency updates should be allowed when multiple pods all have unschedulable conditions",
		},
		{
			name:         "one pod running blocks emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodRunning, []corev1.PodCondition{}, nil),
				createPod("test-sts-1", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient cpu.",
					},
				}, nil),
			},
			expectedResult: false,
			description:    "Emergency updates should be blocked when at least one pod is running",
		},
		{
			name:         "pod schedulable blocks emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:   corev1.PodScheduled,
						Status: corev1.ConditionTrue,
					},
				}, nil),
			},
			expectedResult: false,
			description:    "Emergency updates should be blocked when a pod is pending but schedulable",
		},
		{
			name:         "pod pending for non-resource reason blocks emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "TaintToleration",
						Message: "0/3 nodes are available: 3 node(s) had taint {key: value}, that the pod didn't tolerate.",
					},
				}, nil),
			},
			expectedResult: false,
			description:    "Emergency updates should be blocked when a pod is pending for non-resource reasons",
		},
		{
			name:         "mixed pod states block emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodRunning, []corev1.PodCondition{}, nil),
				createPod("test-sts-1", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient cpu.",
					},
				}, nil),
			},
			expectedResult: false,
			description:    "Emergency updates should be blocked when some pods are unschedulable and some are running",
		},
		{
			name:         "pods being deleted are ignored",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodRunning, []corev1.PodCondition{}, &now),
				createPod("test-sts-1", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient cpu.",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "Pods with deletion timestamp should be ignored",
		},
		{
			name:         "insufficient in reason allows emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "InsufficientResources",
						Message: "Not enough resources available",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "'Insufficient' in the reason should trigger emergency update",
		},
		{
			name:           "list pods error blocks emergency update",
			currentImage:   "typesense/typesense:0.24.0",
			desiredImage:   "typesense/typesense:0.24.0",
			pods:           []*corev1.Pod{},
			withListError:  true,
			expectedResult: false,
			description:    "The function should return false when listing pods fails",
		},
		{
			name:         "pod in succeeded phase blocks emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodSucceeded, []corev1.PodCondition{}, nil),
			},
			expectedResult: false,
			description:    "Emergency updates should be blocked when a pod is in Succeeded phase",
		},
		{
			name:         "complex scenario with multiple pod states",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodRunning, []corev1.PodCondition{}, &now),
				createPod("test-sts-1", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: "0/3 nodes are available: 3 Insufficient cpu.",
					},
				}, nil),
				createPod("test-sts-2", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "InsufficientMemory",
						Message: "Not enough memory",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "Emergency updates should be allowed when all non-deleting pods are unschedulable",
		},
		{
			name:         "pod with failed phase blocks emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodFailed, []corev1.PodCondition{}, nil),
			},
			expectedResult: false,
			description:    "Emergency updates should be blocked when a pod is in Failed phase",
		},
		{
			name:         "unschedulable in message allows emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "ResourceIssue",
						Message: "Pod is Unschedulable due to insufficient resources",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "'Unschedulable' in the reason should trigger emergency update",
		},
		{
			name:         "insufficient in message allows emergency update",
			currentImage: "typesense/typesense:0.24.0",
			desiredImage: "typesense/typesense:0.24.0",
			pods: []*corev1.Pod{
				createPod("test-sts-0", corev1.PodPending, []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "SchedulingIssue",
						Message: "Insufficient resources to schedule pod",
					},
				}, nil),
			},
			expectedResult: true,
			description:    "'Insufficient' in the message should trigger emergency update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = tsv1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)

			sts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sts",
					Namespace: "default",
				},
				Spec: appsv1.StatefulSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "typesense",
						},
					},
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "typesense",
									Image: tt.currentImage,
								},
							},
						},
					},
				},
			}

			ts := &tsv1alpha1.TypesenseCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: tsv1alpha1.TypesenseClusterSpec{
					Image: tt.desiredImage,
				},
			}

			// Convert []*corev1.Pod to []client.Object for fake client
			objects := make([]client.Object, len(tt.pods))
			for i, pod := range tt.pods {
				objects[i] = pod
			}

			var fakeClient client.Client
			if tt.withListError {
				fakeClient = fake.NewClientBuilder().
					WithScheme(scheme).
					WithInterceptorFuncs(errorInterceptorFuncs()).
					Build()
			} else {
				fakeClient = fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(objects...).
					Build()
			}

			reconciler := &TypesenseClusterReconciler{
				Client: fakeClient,
				Scheme: scheme,
				logger: logr.Discard(),
			}

			ctx := context.Background()
			result := reconciler.isSafeToEmergencyUpdate(ctx, sts, ts)

			if result != tt.expectedResult {
				t.Errorf("%s: expected %v, got %v", tt.description, tt.expectedResult, result)
			}
		})
	}
}

// createPod is a helper function to create a pod with the specified parameters
func createPod(name string, phase corev1.PodPhase, conditions []corev1.PodCondition, deletionTimestamp *metav1.Time) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				"app": "typesense",
			},
		},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: conditions,
		},
	}

	if deletionTimestamp != nil {
		pod.DeletionTimestamp = deletionTimestamp
		// Add a finalizer to make the fake client accept the deletion timestamp
		pod.Finalizers = []string{"test.typesense.io/finalizer"}
	}

	return pod
}

// errorInterceptorFuncs returns interceptor functions that cause List operations to fail
func errorInterceptorFuncs() interceptor.Funcs {
	return interceptor.Funcs{
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return fmt.Errorf("simulated list error")
		},
	}
}
