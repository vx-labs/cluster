package cluster

import (
	"context"
	"fmt"

	"github.com/hashicorp/memberlist"
	"github.com/vx-labs/cluster/membership"
	"github.com/vx-labs/cluster/raft"
	"google.golang.org/grpc"
)

type Node interface {
	Run(context.Context)
	RunFromAppliedIndex(ctx context.Context, idx uint64)
	Shutdown() error
	Apply(context.Context, []byte) (uint64, error)
	Ready() <-chan struct{}
	Call(id uint64, f func(*grpc.ClientConn) error) error
	Index() uint64
}
type MultiNode interface {
	Call(id uint64, f func(*grpc.ClientConn) error) error
	Gossip() membership.Pool
	Node(cluster string, config RaftConfig) Node
	Shutdown() error
}

type NodeConfig struct {
	ID            uint64
	ServiceName   string
	DataDirectory string
	Version       string
	GossipConfig  GossipConfig
	RaftConfig    RaftConfig
}

type RaftConfig struct {
	ExpectedNodeCount         int
	AppliedIndex              uint64
	DisableProposalForwarding bool
	LeaderFunc                func(context.Context, raft.RaftStatusProvider) error
	Network                   NetworkConfig
	GetStateSnapshot          func() ([]byte, error)
	CommitApplier             raft.CommitApplier
	SnapshotApplier           raft.SnapshotApplier
	ConfChangeApplier         raft.ConfChangeApplier
	OnNodeRemoved             func(id uint64, leader bool)
}
type NetworkConfig struct {
	AdvertizedHost string
	AdvertizedPort int
	ListeningPort  int
}

func (n NetworkConfig) AdvertizedAddress() string {
	return fmt.Sprintf("%s:%d", n.AdvertizedHost, n.AdvertizedPort)
}

type GossipConfig struct {
	JoinList                 []string
	Network                  NetworkConfig
	DistributedStateDelegate memberlist.Delegate
	NodeEventDelegate        membership.Recorder
}
