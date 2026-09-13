package controller

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

func TestIsNodeReadyForTraffic(t *testing.T) {
	tests := []struct {
		state NodeState
		want  bool
	}{
		{LeaderState, true},
		{FollowerState, true},
		{CandidateState, true},
		{ErrorState, false},
		{NotReadyState, false},
		{UnreachableState, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := isNodeReadyForTraffic(tt.state); got != tt.want {
				t.Fatalf("isNodeReadyForTraffic(%s) = %t, want %t", tt.state, got, tt.want)
			}
		})
	}
}

func TestCalculatePodReadinessGatePreservesHealthCheckForNonTrafficReadyState(t *testing.T) {
	healthCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}

		healthCalled = true
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	node, ts := testNodeAndCluster(t, server.URL)
	reconciler := &TypesenseClusterReconciler{InCluster: true}
	condition := reconciler.calculatePodReadinessGate(
		context.Background(),
		server.Client(),
		node,
		NodeStatus{State: ErrorState},
		ts,
		"",
	)

	if !healthCalled {
		t.Fatal("expected health endpoint to be called")
	}
	if condition.Status != v1.ConditionFalse {
		t.Fatalf("condition.Status = %s, want %s", condition.Status, v1.ConditionFalse)
	}
	if condition.Reason != string(nodeNotHealthy) {
		t.Fatalf("condition.Reason = %s, want %s", condition.Reason, nodeNotHealthy)
	}
}

func TestCalculatePodReadinessGateKeepsNonRecoverableReasonForNonTrafficReadyState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"resource_error":"OUT_OF_MEMORY"}`)
	}))
	defer server.Close()

	node, ts := testNodeAndCluster(t, server.URL)
	reconciler := &TypesenseClusterReconciler{InCluster: true}
	condition := reconciler.calculatePodReadinessGate(
		context.Background(),
		server.Client(),
		node,
		NodeStatus{State: ErrorState},
		ts,
		"",
	)

	if condition.Status != v1.ConditionFalse {
		t.Fatalf("condition.Status = %s, want %s", condition.Status, v1.ConditionFalse)
	}
	if condition.Reason != string(nodeNotRecoverable) {
		t.Fatalf("condition.Reason = %s, want %s", condition.Reason, nodeNotRecoverable)
	}
}

func testNodeAndCluster(t *testing.T, rawURL string) (NodeEndpoint, *tsv1alpha1.TypesenseCluster) {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split test server host port: %v", err)
	}

	apiPort, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	return NodeEndpoint{
			PodName: "typesense-0",
			IP:      net.ParseIP(host),
		}, &tsv1alpha1.TypesenseCluster{
			Spec: tsv1alpha1.TypesenseClusterSpec{
				ApiPort: apiPort,
			},
		}
}
