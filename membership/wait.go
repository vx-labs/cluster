package membership

import (
	"context"
	"errors"
	"time"

	api "github.com/vx-labs/cluster/clusterpb"
	"github.com/vx-labs/cluster/raft"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

var (
	ErrExistingClusterFound = errors.New("existing cluster found")
)

type MemberlistMemberProvider interface {
	Members() []api.RaftContext
}

func (p *pool) WaitForNodes(ctx context.Context, clusterName, nodeName string, expectedNumber int, rpcDialer func(address string, opts ...grpc.DialOption) (*grpc.ClientConn, error)) ([]raft.Peer, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	clusterFound := false
	for {
		for {
			clusterChecked := 0
			nodes := p.mlist.Members()
			for idx := range nodes {
				md, err := DecodeMD(nodes[idx].Meta)
				if err != nil {
					continue
				}
				if md.ClusterName != clusterName {
					continue
				}
				conn, err := rpcDialer(md.RPCAddress)
				if err != nil {
					if err != context.DeadlineExceeded {
						p.logger.Debug("failed to dial peer", zap.Error(err))
					}
					continue
				}
				ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
				out, err := api.NewMultiRaftClient(conn).GetStatus(ctx, &api.GetStatusRequest{ClusterID: nodeName})
				cancel()
				if err != nil {
					p.logger.Debug("failed to get peer status", zap.String("remote_address", md.RPCAddress), zap.Error(err))
					continue
				}
				if md.ID != p.id && out.HasBeenBootstrapped {
					clusterFound = true
				}
				clusterChecked++
			}
			if clusterChecked >= expectedNumber {
				peers := make([]raft.Peer, len(nodes))
				for idx := range peers {
					md, err := DecodeMD(nodes[idx].Meta)
					if err != nil {
						return nil, err
					}
					if md.ClusterName != clusterName {
						continue
					}
					peers[idx] = raft.Peer{Address: md.RPCAddress, ID: md.ID}
				}
				if clusterFound {
					return peers, ErrExistingClusterFound
				}
				return peers, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	}
}
