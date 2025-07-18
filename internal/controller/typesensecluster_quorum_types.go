package controller

import (
	v1 "k8s.io/api/core/v1"
	"net"
)

type NodeState string

const (
	LeaderState      NodeState = "LEADER"
	FollowerState    NodeState = "FOLLOWER"
	CandidateState   NodeState = "CANDIDATE"
	NotReadyState    NodeState = "NOT_READY"
	ErrorState       NodeState = "ERROR"
	UnreachableState NodeState = "UNREACHABLE"
)

type NodeStatus struct {
	CommittedIndex int       `json:"committed_index"`
	QueuedWrites   int       `json:"queued_writes"`
	State          NodeState `json:"state"`
}

type ClusterStatus string

const (
	ClusterStatusOK               ClusterStatus = "OK"
	ClusterStatusSplitBrain       ClusterStatus = "SPLIT_BRAIN"
	ClusterStatusNotReady         ClusterStatus = "NOT_READY"
	ClusterStatusElectionDeadlock ClusterStatus = "ELECTION_DEADLOCK"
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

type NodeEndpoint struct {
	PodName string
	IP      net.IP
}

type Quorum struct {
	MinRequiredNodes   int
	AvailableNodes     int
	Nodes              map[string]net.IP
	NodesListConfigMap *v1.ConfigMap
}
