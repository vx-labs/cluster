package cluster

import (
	"context"
	"path"
	"sync"
	"time"

	"github.com/vx-labs/cluster/clusterpb"
	"github.com/vx-labs/cluster/membership"
	"github.com/vx-labs/cluster/raft"
	"github.com/vx-labs/cluster/topology"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type multinode struct {
	mtx      sync.RWMutex
	nodes    map[string]*node
	gossip   membership.Pool
	logger   *zap.Logger
	recorder topology.Recorder
	config   NodeConfig
	dialer   func(address string, opts ...grpc.DialOption) (*grpc.ClientConn, error)
}

func (n *multinode) Shutdown() error {
	return n.gossip.Shutdown()
}

func NewMultiNode(config NodeConfig, dialer func(address string, opts ...grpc.DialOption) (*grpc.ClientConn, error), server *grpc.Server, logger *zap.Logger) MultiNode {
	recorder := topology.NewRecorder(logger)
	gossipNetworkConfig := config.GossipConfig.Network
	joinList := config.GossipConfig.JoinList
	gossip := membership.New(config.ID,
		config.ServiceName,
		gossipNetworkConfig.ListeningPort, gossipNetworkConfig.AdvertizedHost, gossipNetworkConfig.AdvertizedPort,
		config.RaftConfig.Network.AdvertizedPort,
		dialer, recorder, config.GossipConfig.DistributedStateDelegate, config.GossipConfig.NodeEventDelegate, config.Version, logger)

	rpcAddress := config.RaftConfig.Network.AdvertizedAddress()

	gossip.UpdateMetadata(membership.EncodeMD(config.ID,
		config.ServiceName,
		rpcAddress,
		config.Version,
	))

	if len(joinList) > 0 {
		joinStarted := time.Now()
		retryTicker := time.NewTicker(3 * time.Second)
		for {
			err := gossip.Join(joinList)
			if err != nil {
				logger.Warn("failed to join gossip mesh", zap.Error(err))
			} else {
				break
			}
			<-retryTicker.C
		}
		retryTicker.Stop()
		logger.Debug("joined gossip mesh",
			zap.Duration("gossip_join_duration", time.Since(joinStarted)), zap.Strings("gossip_node_list", joinList))
	}

	clusterpb.RegisterNodeServer(server, newNodeRPCServer())

	m := &multinode{
		config:   config,
		nodes:    map[string]*node{},
		gossip:   gossip,
		logger:   logger,
		recorder: recorder,
		dialer:   dialer,
	}

	clusterpb.RegisterMultiRaftServer(server, m)
	return m
}
func (n *multinode) Gossip() membership.Pool {
	return n.gossip
}
func (n *multinode) Call(id uint64, f func(*grpc.ClientConn) error) error {
	return n.gossip.Call(id, f)
}

func (n *multinode) Node(cluster string, raftConfig RaftConfig) Node {
	n.mtx.Lock()
	defer n.mtx.Unlock()

	raftNode := raft.NewNode(raft.Config{
		NodeID:            n.config.ID,
		ClusterID:         cluster,
		DataDir:           path.Join(n.config.DataDirectory, "nodes", cluster),
		GetSnapshot:       raftConfig.GetStateSnapshot,
		CommitApplier:     raftConfig.CommitApplier,
		SnapshotApplier:   raftConfig.SnapshotApplier,
		ConfChangeApplier: raftConfig.ConfChangeApplier,
	}, n.gossip, n.recorder, n.logger.With(zap.String("cluster_node_name", cluster)))

	clusterList := make([]string, len(n.nodes))
	idx := 0
	for cluster := range n.nodes {
		clusterList[idx] = cluster
		idx++
	}
	n.gossip.UpdateMetadata(membership.EncodeMD(n.config.ID,
		n.config.ServiceName,
		n.config.RaftConfig.Network.AdvertizedAddress(),
		n.config.Version,
	))

	config := n.config
	config.RaftConfig = raftConfig

	n.nodes[cluster] = &node{
		raft:     raftNode,
		cluster:  cluster,
		config:   config,
		dialer:   n.dialer,
		gossip:   n.gossip,
		logger:   n.logger,
		recorder: n.recorder,
		ready:    make(chan struct{}),
	}
	return n.nodes[cluster]
}

func (n *multinode) RemoveMember(ctx context.Context, in *clusterpb.RemoveMultiRaftMemberRequest) (*clusterpb.RemoveMultiRaftMemberResponse, error) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	instance, ok := n.nodes[in.ClusterID]
	if !ok {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	if instance.config.RaftConfig.OnNodeRemoved != nil {
		instance.config.RaftConfig.OnNodeRemoved(in.ID, instance.raft.IsLeader())
	}

	err := instance.raft.RemoveMember(ctx, in.ID, in.Force)
	return &clusterpb.RemoveMultiRaftMemberResponse{}, err
}
func (n *multinode) ProcessMessage(ctx context.Context, in *clusterpb.ProcessMessageRequest) (*clusterpb.Payload, error) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	instance, ok := n.nodes[in.ClusterID]
	if !ok {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	err := instance.raft.ProcessMessage(ctx, in.Message)
	return &clusterpb.Payload{}, err
}
func (n *multinode) GetMembers(ctx context.Context, in *clusterpb.GetMembersRequest) (*clusterpb.GetMembersResponse, error) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	instance, ok := n.nodes[in.ClusterID]
	if !ok {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	return instance.raft.GetClusterMembers()
}
func (n *multinode) GetStatus(ctx context.Context, in *clusterpb.GetStatusRequest) (*clusterpb.GetStatusResponse, error) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	instance, ok := n.nodes[in.ClusterID]
	if !ok {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	return instance.raft.GetStatus(ctx), nil
}
func (n *multinode) JoinCluster(ctx context.Context, in *clusterpb.JoinClusterRequest) (*clusterpb.JoinClusterResponse, error) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	instance, ok := n.nodes[in.ClusterID]
	if !ok {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	return &clusterpb.JoinClusterResponse{Commit: instance.raft.CommittedIndex()},
		instance.raft.AddLearner(ctx, in.Context.ID, in.Context.Address)
}
func (n *multinode) GetTopology(ctx context.Context, in *clusterpb.GetTopologyRequest) (*clusterpb.GetTopologyResponse, error) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	instance, ok := n.nodes[in.ClusterID]
	if !ok {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	return instance.raft.GetTopology(ctx, in)
}
func (n *multinode) PromoteMember(ctx context.Context, in *clusterpb.PromoteMemberRequest) (*clusterpb.PromoteMemberResponse, error) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	instance, ok := n.nodes[in.ClusterID]
	if !ok {
		return nil, status.Error(codes.NotFound, "cluster not found")
	}
	err := instance.raft.PromoteMember(ctx, in.Context.ID, in.Context.Address)

	if err == nil && instance.config.RaftConfig.OnNodeAdded != nil {
		instance.config.RaftConfig.OnNodeAdded(in.Context.ID, instance.raft.IsLeader())
	}

	return &clusterpb.PromoteMemberResponse{}, err
}
