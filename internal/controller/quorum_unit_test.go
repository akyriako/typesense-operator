package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestShouldPurgeSingleNode(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	podWithTypesense := func(state corev1.ContainerState) *corev1.Pod {
		return &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "metrics-exporter", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-time.Hour))}}},
			{Name: containerTypesense, State: state},
		}}}
	}
	runningSince := func(d time.Duration) *corev1.Pod {
		return podWithTypesense(corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-d))}})
	}

	cases := []struct {
		name  string
		pod   *corev1.Pod
		state NodeState
		want  bool
	}{
		{"error state purges immediately", runningSince(time.Second), ErrorState, true},
		{"unreachable while pulling image", podWithTypesense(corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}), UnreachableState, false},
		{"unreachable right after start", runningSince(30 * time.Second), UnreachableState, false},
		{"unreachable after grace period", runningSince(singleNodeStartupGracePeriod), UnreachableState, true},
		{"unreachable crashed container is left to the kubelet", podWithTypesense(corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 139}}), UnreachableState, false},
		{"not ready is never purged", runningSince(time.Hour), NotReadyState, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldPurgeSingleNode(tc.pod, tc.state, now); got != tc.want {
				t.Errorf("shouldPurgeSingleNode() = %v, want %v", got, tc.want)
			}
		})
	}
}
