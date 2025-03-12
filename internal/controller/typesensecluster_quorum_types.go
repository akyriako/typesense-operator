package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
)

type NodeState string

const (
	LeaderState    NodeState = "LEADER"
	FollowerState  NodeState = "FOLLOWER"
	CandidateState NodeState = "CANDIDATE"
	NotReadyState  NodeState = "NOT_READY"
)

type NodeStatus struct {
	CommittedIndex int       `json:"committed_index"`
	QueuedWrites   int       `json:"queued_writes"`
	State          NodeState `json:"state"`
}

type ClusterStatus string

const (
	ClusterStatusOK         ClusterStatus = "OK"
	ClusterStatusSplitBrain ClusterStatus = "SPLIT_BRAIN"
	ClusterStatusNotReady   ClusterStatus = "NOT_READY"
)

type NodeHealthResourceError string

const (
	OutOfMemory NodeHealthResourceError = "OUT_OF_MEMORY"
	OutOfDisk   NodeHealthResourceError = "OUT_OF_DISK"
)

type NodeHealth struct {
	Ok            bool                     `json:"ok"`
	ResourceError *NodeHealthResourceError `json:"resource_error,omitempty"`
}

type Quorum struct {
	MinRequiredNodes   int
	AvailableNodes     int
	HealthyNodes       int
	Nodes              []string
	NodesListConfigMap *v1.ConfigMap
	StatefulSet        *appsv1.StatefulSet
}
