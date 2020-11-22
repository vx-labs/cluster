package cluster

import (
	"context"
	"time"

	"github.com/vx-labs/cluster/clusterpb"
	"github.com/vx-labs/cluster/membership"
	"github.com/vx-labs/cluster/raft"
	"github.com/vx-labs/cluster/topology"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

type node struct {
	cluster  string
	raft     *raft.RaftNode
	recorder topology.Recorder
	gossip   membership.Pool
	logger   *zap.Logger
	config   NodeConfig
	dialer   func(address string, opts ...grpc.DialOption) (*grpc.ClientConn, error)
	ready    chan struct{}
}

func (n *node) Call(id uint64, f func(*grpc.ClientConn) error) error {
	return n.gossip.Call(id, f)
}
func (n *node) Apply(ctx context.Context, event []byte) (uint64, error) {
	return n.raft.Apply(ctx, event)
}
func (n *node) Ready() <-chan struct{} {
	return n.ready
}
func (n *node) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	select {
	case <-n.ready:
	case <-ctx.Done():
		return context.Canceled
	}
	err := n.raft.Leave(ctx)
	if err != nil {
		return err
	}
	if n.cluster != "" {
		return nil
	}
	return n.gossip.Shutdown()
}

func (n *node) isVoter(ctx context.Context, leader uint64) bool {
	var out *clusterpb.GetTopologyResponse
	err := n.gossip.Call(leader, func(c *grpc.ClientConn) error {
		var err error
		out, err = clusterpb.NewMultiRaftClient(c).GetTopology(ctx, &clusterpb.GetTopologyRequest{
			ClusterID: n.cluster,
		})
		return err
	})
	if err != nil {
		return false
	}
	for _, p := range out.Members {
		if p.ID == n.config.ID {
			return p.IsVoter
		}
	}
	return false
}
func (n *node) Index() uint64 {
	return n.raft.CommittedIndex()
}
func (n *node) RunFromAppliedIndex(ctx context.Context, idx uint64) {
	n.config.RaftConfig.AppliedIndex = idx
	n.Run(ctx)
}
func (n *node) Run(ctx context.Context) {
	defer n.logger.Debug("raft node stopped")
	join := false
	peers := raft.Peers{}
	var err error
	if expectedCount := n.config.RaftConfig.ExpectedNodeCount; expectedCount > 1 {
		n.logger.Debug("waiting for nodes to be discovered", zap.Int("expected_node_count", expectedCount))
		peers, err = n.gossip.WaitForNodes(ctx, n.config.ServiceName, n.cluster, expectedCount, n.dialer)
		if err != nil {
			if err == membership.ErrExistingClusterFound {
				n.logger.Debug("discovered existing raft cluster")
				join = true
			} else {
				n.logger.Fatal("failed to discover nodes on gossip mesh", zap.Error(err))
			}
		}
		n.logger.Debug("discovered nodes on gossip mesh", zap.Int("discovered_node_count", len(peers)))
	} else {
		n.logger.Debug("skipping raft node discovery: expected node count is below 1", zap.Int("expected_node_count", expectedCount))
	}
	if join {
		n.logger.Debug("joining raft cluster", zap.Array("raft_peers", peers))
	} else {
		n.logger.Debug("bootstraping raft cluster", zap.Array("raft_peers", peers))
	}
	go func() {
		defer close(n.ready)
		select {
		case <-n.raft.Ready():
			if join {
				ticker := time.NewTicker(1 * time.Second)
				defer ticker.Stop()
				for {
					if n.raft.IsLeader() || n.isVoter(ctx, n.raft.Leader()) {
						n.logger.Debug("local node is now a cluster member")
						return
					}
					for _, peer := range peers {
						if peer.ID == n.config.ID {
							continue
						}
						var clusterIndex uint64
						var err error
						err = n.gossip.Call(peer.ID, func(c *grpc.ClientConn) error {
							out, err := clusterpb.NewMultiRaftClient(c).JoinCluster(ctx, &clusterpb.JoinClusterRequest{
								ClusterID: n.cluster,
								Context: &clusterpb.RaftContext{
									ID:      n.config.ID,
									Address: n.config.RaftConfig.Network.AdvertizedAddress(),
								},
							})
							if err == nil {
								clusterIndex = out.Commit
							}
							return err
						})
						if err != nil {
							if s, ok := status.FromError(err); ok && s.Message() == "node is already a voter" {
								n.logger.Debug("joined cluster as voter")
								return
							}
							n.logger.Debug("failed to join raft cluster, retrying", zap.Error(err))
						} else {
							n.logger.Debug("joined cluster")
							for {
								applied := n.raft.AppliedIndex()
								if clusterIndex == 0 || (applied > 0 && applied >= clusterIndex) {
									err := n.gossip.Call(n.raft.Leader(), func(c *grpc.ClientConn) error {
										_, err := clusterpb.NewMultiRaftClient(c).PromoteMember(ctx, &clusterpb.PromoteMemberRequest{
											ClusterID: n.cluster,
											Context: &clusterpb.RaftContext{
												ID:      n.config.ID,
												Address: n.config.RaftConfig.Network.AdvertizedAddress(),
											},
										})
										return err
									})
									if err != nil {
										n.logger.Error("failed to promote local node", zap.Error(err), zap.Uint64("cluster_index", clusterIndex), zap.Uint64("index", applied))
									} else {
										timeout := time.After(5 * time.Second)
									waitPromote:
										for {
											if n.isVoter(ctx, n.raft.Leader()) {
												n.logger.Info("state machine is up-to-date", zap.Uint64("cluster_index", clusterIndex), zap.Uint64("index", applied))
												return
											}
											select {
											case <-ticker.C:
											case <-timeout:
												break waitPromote
											case <-ctx.Done():
												return
											}
										}
									}
								} else {
									n.logger.Debug("state machine is still not up-to-date", zap.Uint64("cluster_index", clusterIndex), zap.Uint64("index", applied))
								}
								select {
								case <-ticker.C:
								case <-ctx.Done():
									return
								}
							}
						}
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						return
					}
				}
			}
		case <-ctx.Done():
			return
		}
	}()
	n.raft.Run(ctx, peers, join, raft.NodeConfig{
		AppliedIndex:              n.config.RaftConfig.AppliedIndex,
		DisableProposalForwarding: n.config.RaftConfig.DisableProposalForwarding,
		LeaderFunc:                n.config.RaftConfig.LeaderFunc,
	})
}
