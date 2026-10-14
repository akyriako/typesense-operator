package controller

import (
	"testing"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

const testClusterName = "foo"

func TestGetMinimumRequiredNodes(t *testing.T) {
	cases := map[int]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 3, 6: 4, 7: 4}
	for nodes, want := range cases {
		if got := getMinimumRequiredNodes(nodes); got != want {
			t.Errorf("getMinimumRequiredNodes(%d) = %d, want %d", nodes, got, want)
		}
	}
}

func TestWithoutKeysMatchesExactly(t *testing.T) {
	in := map[string]string{
		"app":                       "ts-sts",
		"app.kubernetes.io/part-of": "search",
		teamKey:                     "platform",
	}

	got := withoutKeys(in, "app")
	if _, ok := got["app"]; ok {
		t.Errorf("expected key %q to be removed", "app")
	}
	if got["app.kubernetes.io/part-of"] != "search" || got[teamKey] != "platform" {
		t.Errorf("expected unrelated keys to be kept, got %v", got)
	}
}

func TestIsReferenceGrantOwnedBy(t *testing.T) {
	ts := &tsv1alpha1.TypesenseCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: "a"}}

	grantFrom := func(namespace string) *gatewayv1beta1.ReferenceGrant {
		return &gatewayv1beta1.ReferenceGrant{
			Spec: gatewayv1beta1.ReferenceGrantSpec{
				From: []gatewayv1beta1.ReferenceGrantFrom{{
					Group:     gatewayv1beta1.GroupName,
					Kind:      kindHTTPRoute,
					Namespace: gatewayv1beta1.Namespace(namespace),
				}},
			},
		}
	}

	if !isReferenceGrantOwnedBy(grantFrom("a"), ts) {
		t.Error("expected grant from the cluster's namespace to be owned")
	}
	if isReferenceGrantOwnedBy(grantFrom("b"), ts) {
		t.Error("expected grant of an equally named cluster in another namespace not to be owned")
	}
}

func TestReferenceGrantNameIncludesNamespace(t *testing.T) {
	a := &tsv1alpha1.TypesenseCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: "a"}}
	b := &tsv1alpha1.TypesenseCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: "b"}}

	if getReferenceGrantName(a, "api") == getReferenceGrantName(b, "api") {
		t.Error("expected reference grant names of equally named clusters in different namespaces to differ")
	}
}
