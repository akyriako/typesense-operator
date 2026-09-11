package controller

import "testing"

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
