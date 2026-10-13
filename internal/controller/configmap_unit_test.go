package controller

import (
	"context"
	"testing"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// An endpoint listed in two slices must count once; otherwise a peer list with a missing pod passes
// the replica count check and ends up in the nodes config map.
func TestGetNodesDeduplicatesEndpointsAcrossSlices(t *testing.T) {
	const ns = "a"
	ts := &tsv1alpha1.TypesenseCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: ns},
		Spec:       tsv1alpha1.TypesenseClusterSpec{Replicas: 3, ApiPort: 8108, PeeringPort: 8107},
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "foo-sts", Namespace: ns},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr.To[int32](3),
			ServiceName: "foo-sts-svc",
			Selector:    &metav1.LabelSelector{MatchLabels: getLabels(ts)},
		},
	}

	pod := func(name, ip string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: getLabels(ts)},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip},
		}
	}

	slice := func(name string, ips ...string) *discoveryv1.EndpointSlice {
		s := &discoveryv1.EndpointSlice{
			ObjectMeta:  metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{discoveryv1.LabelServiceName: "foo-sts-svc"}},
			AddressType: discoveryv1.AddressTypeIPv4,
		}
		for _, ip := range ips {
			s.Endpoints = append(s.Endpoints, discoveryv1.Endpoint{
				Addresses: []string{ip},
				TargetRef: &corev1.ObjectReference{Kind: "Pod"},
			})
		}
		return s
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// foo-sts-2 is missing; 10.0.0.2 is listed in both slices.
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		sts,
		pod("foo-sts-0", "10.0.0.1"),
		pod("foo-sts-1", "10.0.0.2"),
		slice("slice-a", "10.0.0.1", "10.0.0.2"),
		slice("slice-b", "10.0.0.2"),
	).Build()

	r := &TypesenseClusterReconciler{Client: c}
	nodes, err := r.getNodes(context.Background(), ts, 3, false)
	if err != nil {
		t.Fatalf("getNodes failed: %v", err)
	}

	want := []string{"10.0.0.1:8107:8108", "10.0.0.2:8107:8108"}
	if len(nodes) != len(want) || nodes[0] != want[0] || nodes[1] != want[1] {
		t.Errorf("getNodes() = %v, want %v", nodes, want)
	}
}
