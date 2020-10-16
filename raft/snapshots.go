package raft

import (
	"log"

	"go.etcd.io/etcd/raft"
	"go.uber.org/zap"
)

func (rc *RaftNode) maybeTriggerSnapshot(committedIndex uint64) {
	rc.progressMu.Lock()
	defer rc.progressMu.Unlock()

	if committedIndex < rc.snapCount || committedIndex < rc.progress.snapshotIndex {
		return
	}
	if committedIndex-rc.progress.snapshotIndex <= rc.snapCount {
		return
	}
	snapIndex := committedIndex - rc.snapCount

	data, err := rc.getStateSnapshot()
	if err != nil {
		log.Panic(err)
	}

	snap, err := rc.raftStorage.CreateSnapshot(committedIndex, &rc.progress.confState, data)
	if err != nil {
		if err == raft.ErrSnapOutOfDate {
			return
		}
		panic(err)
	}
	if err := rc.saveSnap(snap); err != nil {
		panic(err)
	}

	if err := rc.raftStorage.Compact(snapIndex); err != nil {
		panic(err)
	}

	rc.logger.Debug("compacted log", zap.Uint64("compact_index", snapIndex))
	rc.progress.snapshotIndex = committedIndex
}
