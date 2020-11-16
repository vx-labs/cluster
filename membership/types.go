package membership

import (
	"context"

	"github.com/hashicorp/memberlist"
	api "github.com/vx-labs/cluster/clusterpb"
	"github.com/vx-labs/cluster/raft"
	"google.golang.org/grpc"
)

// Pool builds a group of nodes.
// It handles new nodes discovery, failed nodes evinction and node's metadata updates propagations.
type Pool interface {
	Call(id uint64, f func(*grpc.ClientConn) error) error
	UpdateMetadata(meta []byte)
	NodeMeta(limit int) []byte
	Join(hosts []string) error
	Members() []*api.Member
	MemberCount() int
	GossipMembers() []*memberlist.Node
	Shutdown() error
	WaitForNodes(ctx context.Context, clusterName, nodeName string, expectedNumber int, rpcDialer func(address string, opts ...grpc.DialOption) (*grpc.ClientConn, error)) ([]raft.Peer, error)
}

// Recorder records membership changes
type Recorder interface {
	NotifyGossipJoin(id uint64)
	NotifyGossipLeave(id uint64)
}
