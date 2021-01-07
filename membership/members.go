package membership

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/memberlist"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

var (
	// ErrPeerNotFound indicates that the requested peer does not exist in the pool.
	ErrPeerNotFound = errors.New("peer not found")
	// ErrPeerDisabled indicates that the requested peer has recently failed healthchecks.
	ErrPeerDisabled = errors.New("peer disabled by healthchecks")
)

func parseID(id string) uint64 {
	return binary.BigEndian.Uint64([]byte(id))
}

type member struct {
	Conn      *grpc.ClientConn
	Enabled   bool
	LatencyMs int64
}

// NotifyJoin is called if a peer joins the cluster.
func (p *pool) NotifyJoin(n *memberlist.Node) {
	id := parseID(n.Name)
	if id == p.id {
		return
	}
	p.mtx.Lock()
	md, err := DecodeMD(n.Meta)
	if err != nil {
		p.logger.Error("failed to decode new node meta", zap.String("new_node_id", fmt.Sprintf("%x", id)), zap.Error(err))
		p.mtx.Unlock()
		return
	}
	if md.ID != id {
		p.logger.Error("mismatch between node metadata id and node name", zap.String("new_node_id", fmt.Sprintf("%x", id)), zap.Error(err))
		p.mtx.Unlock()
		return
	}
	old, ok := p.peers[md.ID]
	if ok && old != nil {
		if old.Conn.Target() == md.RPCAddress {
			p.mtx.Unlock()
			return
		}
		old.Conn.Close()
	}
	conn, err := p.rpcDialer(md.RPCAddress)
	if err != nil {
		p.logger.Error("failed to dial new gossip nope", zap.Error(err))
		p.mtx.Unlock()
		return
	}
	p.peers[md.ID] = &member{
		Conn:    conn,
		Enabled: true,
	}
	p.mtx.Unlock()
	if p.recorder != nil {
		p.recorder.NotifyGossipJoin(id)
	}
	if p.eventDelegate != nil {
		p.eventDelegate.NotifyGossipJoin(id)
	}
}

// NotifyLeave is called if a peer leaves the cluster.
func (p *pool) NotifyLeave(n *memberlist.Node) {
	id := parseID(n.Name)
	if id == p.id {
		return
	}
	p.mtx.Lock()
	old, ok := p.peers[id]
	if ok && old != nil {
		old.Conn.Close()
		delete(p.peers, id)
	}
	p.mtx.Unlock()
	if p.recorder != nil {
		p.recorder.NotifyGossipLeave(id)
	}
	if p.eventDelegate != nil {
		p.eventDelegate.NotifyGossipLeave(id)
	}
}

// NotifyUpdate is called if a cluster peer gets updated.
func (p *pool) NotifyUpdate(n *memberlist.Node) {
	p.NotifyJoin(n)
}

func (p *pool) Call(id uint64, f func(*grpc.ClientConn) error) error {
	if id == p.id {
		return errors.New("attempted to contact to local node")
	}
	p.mtx.RLock()
	peer, ok := p.peers[id]
	p.mtx.RUnlock()
	if !ok {
		return ErrPeerNotFound
	}
	if !peer.Enabled {
		return ErrPeerDisabled
	}
	return f(peer.Conn)
}

func (p *pool) runHealthchecks(ctx context.Context) error {
	p.mtx.RLock()
	set := make([]*member, len(p.peers))
	idx := 0
	for _, peer := range p.peers {
		set[idx] = peer
		idx++
	}
	p.mtx.RUnlock()
	for _, peer := range set {
		ctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
		start := time.Now()
		resp, err := healthpb.NewHealthClient(peer.Conn).Check(ctx, &healthpb.HealthCheckRequest{})
		cancel()
		peer.LatencyMs = time.Since(start).Milliseconds()
		if err != nil || resp.Status != healthpb.HealthCheckResponse_SERVING {
			if peer.Enabled {
				peer.Enabled = false
			}
		} else if !peer.Enabled {
			peer.Enabled = true
		}
	}
	return nil
}
